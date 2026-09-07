// Command herosd serves the conversational console and drives durable goals.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/api"
	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/autonomy"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/converse"
	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/mailer"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/provider/qwen"
	"github.com/heros-foreal/heros/internal/router"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/tenancy"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/tools"
	"github.com/heros-foreal/heros/internal/worker"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	web := flag.String("web", defaultWebRoot, "directory holding the console")
	dsn := flag.String("dsn", "", "postgres DSN (default $HEROS_DATABASE_URL)")
	flag.Parse()

	if err := config.LoadDotEnv(".env.local"); err != nil {
		log.Fatalf("config: %v", err)
	}
	key, err := config.QwenKey()
	if err != nil {
		log.Fatalf("%v", err)
	}
	// 🔴 Before anything hashes a password — bootstrap creates the first account, which does. Applying the
	// ceiling afterwards would leave that one call running under a different limit from every call after
	// it, which is the kind of difference nobody finds by reading.
	if err := auth.ConfigureFromEnv(); err != nil {
		log.Fatalf("password hashing: %v", err)
	}
	url := *dsn
	if url == "" {
		url = os.Getenv("HEROS_DATABASE_URL")
	}
	if url == "" {
		url = "postgres://heros:heros@localhost:55700/heros?sslmode=disable"
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("postgres: %v — is it running? try `make pg-up`", err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	st := store.NewPostgres(db)
	plans, err := planner.Default()
	if err != nil {
		log.Fatalf("planners: %v", err)
	}

	// 🔴 The tool registry is built ONCE at startup, and Register refuses a tool that cannot be operated
	// safely. That is why this is a fatal error rather than a per-request one: a deployment missing a
	// verifier for an effect-bearing tool should fail to start, not fail on the first pull request.
	reg := toolcontract.NewRegistry()
	client := qwen.New(key)
	// The endpoint travels WITH the credential — a key issued for one Model Studio host is refused by
	// the others with a 401 that reads like a revoked key. See config.QwenBaseURL for why this exists
	// and why the two variables must move in the same deployment change. Overriding after New keeps
	// qwen.DefaultBaseURL as the single place the default is written, rather than restating it here.
	if base := config.QwenBaseURL(); base != "" {
		client.BaseURL = base
	}
	// The assessment tool is registered with no source: it is REBOUND when a repository is loaded, by
	// Registry.Replace, which re-runs the same refusals. Registering here proves at startup that the
	// tool satisfies them, rather than discovering it on a customer's first question.
	if err := reg.Register(tools.AssessAxis{
		Provider: client, Model: qwen.ModelFlash, MaxTokens: 1200,
		Source: tools.FixtureSource{},
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.SynthesiseAssessment{
		Provider: client, Model: qwen.ModelFlash,
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	// Eval-set generation and comparison. The source-bound ones are REBOUND when a repository loads;
	// registering here proves at startup that they satisfy the contract's refusals rather than
	// discovering it on a customer's first question.
	if err := reg.Register(tools.GenerateCases{
		Provider: client, Model: qwen.ModelFlash, Source: tools.FixtureSource{},
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.QualityGate{}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.PublishEvalSet{Root: "."}, tools.NewPublishVerifier(".")); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.CompareAssessments{Store: st, Tenant: "local"}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	// The improvement run's chain: propose, verify, deliver. Source- and root-bound tools are REBOUND
	// when a repository loads; registering here proves at startup that they satisfy the contract's
	// refusals — an effect-bearing tool without a verifier should stop the process, not the customer.
	if err := reg.Register(tools.ProposeChange{
		Provider: client, Model: qwen.ModelFlash, Source: &discovery.Index{}, Root: ".",
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.VerifyProposal{
		Provider: client, Model: qwen.ModelFlash, Root: ".",
	}, nil); err != nil {
		log.Fatalf("tools: %v", err)
	}
	if err := reg.Register(tools.OpenPullRequest{Root: "."}, tools.NewDeliveryVerifier(".")); err != nil {
		log.Fatalf("tools: %v", err)
	}

	mem := memory.NewPG(db)

	cache, _ := os.UserCacheDir()
	authStore := auth.NewStore(db)

	// Workers are built inside SupervisorFor below, one per organization — but they are built AFTER the
	// identity store either way, because a worker's approval policy reads each organization's autonomy
	// setting from it.

	// 🔴 Mail is resolved BEFORE the first request, and a half-configured relay stops the process. A
	// relay with a missing credential accepts connections and delivers nothing, which is indistinguishable
	// from working until a customer cannot reset their password — this product has already run that way
	// for days once. Failing at startup puts the discovery in front of the operator who caused it.
	mail, err := mailer.FromEnv()
	if err != nil {
		log.Fatalf("mail: %v", err)
	}
	var links mailer.Links
	if _, off := mail.(mailer.Unconfigured); !off {
		// Only required when there is something to put a link in. A deployment that has chosen to run
		// without mail should not also have to declare its public address.
		links, err = mailer.NewLinks(os.Getenv("HEROS_PUBLIC_URL"))
		if err != nil {
			log.Fatalf("mail: %v", err)
		}
	}

	if err := bootstrapIdentity(ctx, authStore, mail, links); err != nil {
		log.Fatalf("identity: %v", err)
	}

	srv := api.NewServer()
	srv.Root = st
	srv.Auth = authStore
	{
		cfg := &api.Server{
			Planners: plans,
			// 🔴 A supervisor PER ORGANIZATION, built on first use, each against its own slice of the
			// store. A single one bound to a single organization was correct only while there was
			// exactly one; with self-serve sign-up a goal would be written in the caller's scope and
			// looked for in the boot organization's, and the run would silently never happen.
			//
			// The worker is built per organization too, and for the same reason: it reads and writes
			// tasks through the scope it was constructed with.
			SupervisorFor: func(tenant string) *api.Supervisor {
				return api.NewSupervisor(st.For(tenant),
					buildWorker(st.For(tenant), reg, mem, plans, autonomy.Policy{Source: authStore}))
			},
			Resolver: intake.NewResolver(filepath.Join(cache, "heros", "repos")),
			Router:   router.New(),
			Ceilings: bounds.Ceilings{
				MaxIterations: 200, MaxTasks: 60, MaxAttemptsPerTask: 2, MaxToolCalls: 200,
				MaxTokens: 400_000, MaxCostCents: 100, MaxWallClock: 20 * time.Minute, MaxSpawnDepth: 3,
			},
		}
		srv.Planners, srv.SupervisorFor, srv.Resolver = cfg.Planners, cfg.SupervisorFor, cfg.Resolver
		srv.Router, srv.Ceilings = cfg.Router, cfg.Ceilings
	}
	srv.ToolRegistry = reg
	srv.Provider = client
	srv.Model = qwen.ModelFlash
	// The conversational agent. 🔴 Shares the provider and the model with the tools deliberately: a
	// deployment where the console reasons on one model and the assessments run on another is one where
	// "what did it use?" has two answers and the bill has two lines nobody can reconcile.
	srv.Converse = &converse.Agent{
		Provider: client, Model: qwen.ModelFlash, Bounds: converse.DefaultBounds,
	}
	srv.Episodes = mem
	srv.Mail, srv.Links = mail, links

	httpSrv := &http.Server{
		// 🔴 Handler, not Routes. Routes is unauthenticated by construction and exists for tests;
		// mounting it here would serve every endpoint to anybody who can reach the port.
		Addr: *addr, Handler: srv.Handler(alwaysRevalidate(http.FileServer(http.Dir(*web)))),
		ReadHeaderTimeout: 10 * time.Second,
		// 🚫 No WriteTimeout. The events endpoint is a long-lived stream, and a write deadline would cut
		// every run off mid-flight at exactly the moment it became interesting.
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	fmt.Printf("heros console  http://%s\n", *addr)
	fmt.Printf("model          %s\n", qwen.ModelFlash)
	// 🔴 Printed because it is newly true. Answering a question used to cost nothing, and both the code
	// and the console said so; an operator should see on the first line of a deploy that this changed.
	fmt.Printf("conversation   agent on, up to %d model call(s) per turn, ceiling %s\n",
		converse.DefaultBounds.MaxCalls,
		provider.FormatCents(converse.DefaultBounds.MaxCostMicroCents))
	fmt.Printf("database       %s\n", redact(url))
	fmt.Printf("mail           %s\n", mail.Describe())
	// 🔴 Printed because it is a memory budget, not a tuning detail, and an operator sizing a container
	// needs to see it rather than derive it.
	//
	// ⚠️ "live at once", NOT peak RSS. The first version of this line said "peak" and was wrong by more
	// than a factor of two: Go returns freed memory to the operating system lazily, so RSS climbs to a
	// plateau well above the live bound and stays there. Measured on this machine: a ceiling of 9 (576 MiB
	// live) settles at about 1.4 GB resident and does not grow with further load. A number labelled "peak"
	// is read as a promise about what the container needs, and that promise would have been broken by the
	// first flood.
	fmt.Printf("password work  %d concurrent argon2id verifications (%d MiB live at once), "+
		"shed after %s\n", auth.Concurrency(), auth.Concurrency()*64, auth.MaxWait())
	if links.Origin() != "" {
		fmt.Printf("public url     %s\n", links.Origin())
	}
	if _, isLog := mail.(mailer.LogMailer); isLog {
		// Loud, every boot. This mode prints invitation and reset links into the log, which is a complete
		// account takeover for anybody who can read it — fine on a laptop, catastrophic anywhere else.
		fmt.Println("⚠️  HEROS_MAIL_MODE=log — invitation and password-reset links are written to this " +
			"log instead of being sent. Development only; do not run a deployment this way.")
	}
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// defaultTenant is the organization a single-deployment install acts as.
const defaultTenant = "local"

// defaultWebRoot is the directory holding the console.
//
// 🔴 It pointed at `web`, which holds no index.html — so running the daemon with default flags served a
// DIRECTORY LISTING rather than the console, and every instruction anywhere had to remember to pass
// `-web web/static`. A default that does not work is worse than no default: it fails in a way that looks
// like the product, so the reaction is to doubt the build rather than the flag.
//
// A constant rather than a literal in the flag declaration, so `TestTheDefaultConsoleDirectoryHasAConsole
// InIt` checks the value the daemon actually uses instead of a copy of it.
const defaultWebRoot = "web/static"

// alwaysRevalidate makes every static response conditional.
//
// # 🔴 The bug this exists for
//
// http.FileServer sends `Last-Modified` and nothing else — no `Cache-Control`, no `ETag`. With no
// `Cache-Control` a browser falls back to HEURISTIC freshness: it invents an expiry, commonly a
// tenth of the file's age, and serves the file from cache for that long WITHOUT ASKING THE SERVER.
//
// The console is four files that have to agree with each other — index.html, heros.css,
// heros-theme.js, heros-arc.js. Heuristic caching expires them independently, so a deploy can leave
// an open tab with a new index.html and a cached old heros.css, or the reverse. The page then renders
// as neither version. Nothing is logged, because THE SERVER NEVER SEES A REQUEST: the stale half is
// served out of the browser. It is invisible from here and unreproducible on a cold load, which is
// exactly what happened after three deploys in one hour on 2026-09-02.
//
// `no-cache` does not mean "do not store". It means "store it, but ask before reusing it" — the
// browser keeps the bytes and sends If-Modified-Since, and an unchanged file comes back as a 304 with
// an empty body. So the cost is one conditional request per file per load, and the guarantee is that
// the set is always coherent.
//
// 🚫 NOT `no-store`, which forbids keeping the bytes at all and would re-download the 229KB portrait
// on every navigation. The distinction is the whole point; see TestStaticAssetsAreAlwaysRevalidated.
//
// The stronger fix is content-hashed filenames, which make a stale file impossible rather than
// merely detected — that needs a build step this project does not have, and would be the thing to do
// if the console ever grows one.
func alwaysRevalidate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// bootstrapIdentity creates the first organization and user, once.
//
// 🔴 The password comes from the environment and is never defaulted. A built-in default password is a
// published credential: it is in the source, it is in every deployment, and it is the first thing
// anybody tries. If the variable is unset and no user exists, the process REFUSES TO START and says what
// to set — a server that will not boot is a far better outcome than one that boots with a known login.
//
// 🔴 The first account is an OWNER. It is the only account that can be created without an invitation, so
// if it were anything less the organization would start with nobody able to invite anybody — locked out
// of its own administration by the moment it was created, and recoverable only with database access.
func bootstrapIdentity(ctx context.Context, a *auth.Store, mail mailer.Mailer, links mailer.Links) error {
	n, err := a.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	email := os.Getenv("HEROS_BOOTSTRAP_EMAIL")
	password := os.Getenv("HEROS_BOOTSTRAP_PASSWORD")
	if email == "" || password == "" {
		return fmt.Errorf("no users exist yet and no bootstrap credentials were given.\n"+
			"Set HEROS_BOOTSTRAP_EMAIL and HEROS_BOOTSTRAP_PASSWORD (at least %d characters) and start "+
			"again. They are used once, to create the first account", auth.MinPasswordLength)
	}
	if err := a.CreateTenant(ctx, defaultTenant, "Local"); err != nil {
		return err
	}
	userID, err := a.CreateUser(ctx, defaultTenant, email, password, tenancy.Owner)
	if err != nil {
		return err
	}
	log.Printf("created the first account for %s in organization %q, as owner", email, defaultTenant)

	// A confirmation link, if this deployment can send one. 🔴 Best effort and NOT fatal: nobody has
	// signed in yet, the address was typed by whoever is watching this log, and refusing to start over an
	// unconfirmed address would make a mail outage into a deployment that cannot come up. Confirmation
	// gates nothing — see migration 0006.
	if links.Origin() == "" {
		return nil
	}
	token, to, org, err := a.CreateEmailVerification(ctx, defaultTenant, userID)
	if err != nil {
		log.Printf("WARN auth.verification.create_failed user=%s: %v", userID, err)
		return nil
	}
	msg := mailer.VerifyEmail(to, org, links.Verify(token), auth.EmailVerificationLifetime)
	if err := mail.Send(ctx, msg); err != nil {
		log.Printf("WARN mail.send_failed purpose=%q to=%s: %v — the account works; the address is "+
			"simply unconfirmed, and can be confirmed later from the console", "email verification", to, err)
	}
	return nil
}

// buildWorker assembles the worker with everything it needs.
//
// # 🔴 Why this is a function rather than four lines in main
//
// Because it was four lines in main, and one of them was missing. `Reviser` was never set, so every
// improvement run stopped after its assessment: the plan never grew into proposals, and the goal
// reported SUCCESS because its completion criterion was satisfied by the assessment alone. Nothing was
// red. The end-to-end test passed throughout, because the test wires its own worker — so the tested path
// and the shipped path were different objects assembled by different code.
//
// Extracted so `TestTheDaemonsWorkerIsFullyWired` can assemble the same object the daemon runs.
func buildWorker(st store.Store, reg *toolcontract.Registry, mem memory.Root,
	plans *planner.Registry, policy worker.ApprovalPolicy) *worker.Worker {
	w := worker.New("herosd", st, reg)
	w.Lease = 2 * time.Minute
	w.Episodes = mem
	// Without this an improvement run assesses and stops, and reports success for doing so.
	w.Reviser = plans
	// 🔴 Passed in rather than defaulted, so the daemon's policy is visible at the call site and the
	// fence below can assemble the same object the daemon runs. worker.New's default gates EVERY effect;
	// that is the right default for a worker built without an opinion, and the wrong thing to leave in
	// place silently once the product has a setting for it.
	w.Policy = policy
	return w
}

// redact hides a password in a DSN before it reaches a terminal or a log.
func redact(dsn string) string {
	at := -1
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return dsn
	}
	scheme := 0
	for i := 0; i+2 < len(dsn); i++ {
		if dsn[i] == ':' && dsn[i+1] == '/' && dsn[i+2] == '/' {
			scheme = i + 3
			break
		}
	}
	return dsn[:scheme] + "***" + dsn[at:]
}
