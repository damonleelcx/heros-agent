// Command herossign is the release-signing tool: it generates an ed25519 keypair, signs the SHA256SUMS
// manifest, and verifies a signature. It is the documented verification step's implementation (P11 task
// 6.2/6.4) — the same standard-library primitives the `heros` binary already trusts, so verifying a
// release adds no new tool to a customer's trust surface.
//
//	herossign keygen                                  → prints PUBLIC and PRIVATE hex keys
//	herossign sign   --key <hexpriv> --in SHA256SUMS  → prints the hex signature
//	herossign verify --pub <hexpub>  --in SHA256SUMS --sig SHA256SUMS.sig
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/heros-foreal/agentd/internal/release"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		keygen()
	case "sign":
		sign(os.Args[2:])
	case "verify":
		verify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: herossign keygen | sign --key <hex> --in <file> | verify --pub <hex> --in <file> --sig <file>")
}

func keygen() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("HEROS_RELEASE_PUBLIC_KEY=%s\n", hex.EncodeToString(pub))
	fmt.Printf("HEROS_RELEASE_PRIVATE_KEY=%s\n", hex.EncodeToString(priv))
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	key := fs.String("key", "", "hex ed25519 private key (or $HEROS_RELEASE_PRIVATE_KEY)")
	in := fs.String("in", "SHA256SUMS", "manifest to sign")
	_ = fs.Parse(args)
	k := *key
	if k == "" {
		k = os.Getenv("HEROS_RELEASE_PRIVATE_KEY")
	}
	raw, err := hex.DecodeString(k)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		fatal(fmt.Errorf("invalid private key"))
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fatal(err)
	}
	fmt.Println(release.Sign(ed25519.PrivateKey(raw), data))
}

func verify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pub := fs.String("pub", "", "hex ed25519 public key (or $HEROS_RELEASE_PUBLIC_KEY)")
	in := fs.String("in", "SHA256SUMS", "manifest that was signed")
	sigFile := fs.String("sig", "SHA256SUMS.sig", "detached signature file")
	_ = fs.Parse(args)
	p := *pub
	if p == "" {
		p = os.Getenv("HEROS_RELEASE_PUBLIC_KEY")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fatal(err)
	}
	sig, err := os.ReadFile(*sigFile)
	if err != nil {
		fatal(err)
	}
	if err := release.Verify(p, data, string(sig)); err != nil {
		fmt.Fprintln(os.Stderr, "VERIFICATION FAILED: "+err.Error())
		os.Exit(1)
	}
	fmt.Println("OK: signature verifies and the release is authentic")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "herossign: "+err.Error())
	os.Exit(1)
}
