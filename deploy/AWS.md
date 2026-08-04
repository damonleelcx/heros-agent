# Deploying on AWS (EKS)

The self-contained runbook for standing this platform up on AWS. It assumes you have read
[`README.md`](README.md) — especially **[What a fresh install actually serves](README.md#what-a-fresh-install-actually-serves)**,
because it decides whether several of the components below are worth paying for yet.

Every term is defined before it is used. Every command is one you can run. Where the tree does not yet
contain something this deploy needs, that is stated as a **gap with the manifest you must add**, not
glossed as a step.

---

## 0. Read this before you start: five things the tree does not have

These are not warnings about difficulty. Each one will stop the deploy, and three of them are silent.

| # | Gap | What happens if you skip it |
|---|---|---|
| 1 | **No published images.** [`images.env`](images.env) pins `sha256:0000…` placeholders for `agentd`, `heros-console` and `heros-admin-console` — the release pipeline that replaces them is out of P19's scope. | `ImagePullBackOff` on every platform pod. §2 builds and pushes them to ECR. |
| 2 | **The AWS Load Balancer Controller needs an IAM role of its own**, and installing the chart does not create one. The two Ingresses now ship in `overlays/prod/ingress.yaml` — *(this used to read "no Ingress manifest exists anywhere"; that gap is closed)* — but a controller with no permissions reconciles them to nothing. | Same silent shape as before: the apply *succeeds*, every pod is healthy, `kubectl get ingress` shows **no ADDRESS**, and nothing is reachable from a browser. §7.2. |
| 3 | **The `heros-secrets` ServiceAccount is referenced but never defined.** `overlays/prod/secretstore.yaml` authenticates as it; no manifest creates it. | Every `ExternalSecret` fails, every pod stays `CreateContainerConfigError` waiting for a Secret that never materialises. §5. |
| 4 | 🔴 **EKS does not enforce NetworkPolicy by default.** The VPC CNI ignores `NetworkPolicy` objects unless network-policy support is explicitly enabled. | The apply succeeds and the policies are *inert*. Default-deny, the control/data-plane seam and the **model-call egress allowlist** — Decisions 3 and 4, the security posture this deploy is largely built around — all silently do nothing. §3. |
| 5 | **The AWS secrets source is selected but never located.** The base sets `HEROS_SECRETS_SOURCE=aws-secrets-manager`; until this change nothing set `HEROS_SECRETS_AWS_PREFIX` (or `_IDS`) or a region, and that is resolved **at boot**. | `agentd` exits immediately — *"requires either HEROS_SECRETS_AWS_IDS or HEROS_SECRETS_AWS_PREFIX to locate the secrets"* — and CrashLoopBackOffs. **Fixed in the base now**; listed so you recognise it if you carry an older overlay. |

Gaps 2 and 4 are the ones to take seriously — they are the two that leave you with a deploy that looks
correct. Both have a one-command check (§7.2, §3) and neither is visible without it. Everything else
fails loudly.

---

## 1. Decide two things first

**(a) Postgres: RDS or in-cluster?** The base ships an in-cluster single-replica `StatefulSet` plus a
backup `CronJob`. It works, and it is a documented single point of failure whose backup is its
precondition (Decision 6).

On AWS, **RDS is usually the better call** and the deploy already accommodates it: `agentd` reads one
`DATABASE_URL` and applies its schema at boot through a ledger it reads, so it does not care where the
database lives. If you use RDS, delete `postgres.yaml`'s objects in your overlay — and understand that
you have also deleted the backup `CronJob`, so **RDS automated backups become the thing standing between
you and data loss**. Accepting the SPOF without shipping backup is exactly what Decision 6 forbids;
moving to RDS moves the obligation, it does not remove it.

🔴 **Deleting the workload is only half of it — you must also repoint the egress, and nothing tells you
that you forgot.** The base's `agentd` NetworkPolicy permits 5432 to a **pod selector**, and its only
other egress rule is 443 to public IPs with `10.0.0.0/8`, `172.16.0.0/12` and `192.168.0.0/16`
*explicitly excepted*, so that rule cannot become a backdoor to an in-cluster service. An RDS instance is
a **private VPC address on 5432**: matched by the exception, permitted by nothing. Under default-deny,
`agentd` therefore cannot reach its database at all — and because an unreachable `DATABASE_URL` is a
deliberate hard boot failure, what you see is `CrashLoopBackOff` with a connection timeout, several
layers away from the resource list you edited to choose RDS. The consent-retention `CronJob` has the same
problem for the same reason, and *its* failure is silent.

Both patches ship in [`overlays/prod/kustomization.yaml`](k8s/overlays/prod/kustomization.yaml). Narrow
the CIDR to your DB subnets once the instance exists — the whole VPC on 5432 is wider than the one host
`agentd` needs.

**(b) Do you need the object store, queue, vector store and graph store yet?** They are labelled
*provisioned ahead of use* in the README: **no deployed process dials them today**. On a laptop that
costs nothing. On EKS it is four workloads, four EBS volumes and four things to patch, for a capability
that is not mounted. Dropping them from your overlay until the capability that uses them ships is a
legitimate and cheaper choice — remove them from `resources:` and remove their `*_HEALTH_URL` variables
from `agentd`, so `/readyz` stops aggregating components you deliberately do not run.

---

## 2. Build and push the three images to ECR

The build needs a machine with Docker, this checkout, and network access to your module and package
registries. Everything below is on that machine.

```bash
export AWS_REGION=us-east-1
export ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
export REG=$ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com

for r in agentd heros-console heros-admin-console; do
  aws ecr describe-repositories --repository-names "$r" --region "$AWS_REGION" >/dev/null 2>&1 \
    || aws ecr create-repository --repository-name "$r" --region "$AWS_REGION" \
         --image-scanning-configuration scanOnPush=true \
         --image-tag-mutability IMMUTABLE
done

aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$REG"
```

`IMMUTABLE` is deliberate: a mutable tag means the image you audited is not necessarily the image that
runs, which is the property digest pinning exists to buy.

Build. `GOPROXY` is passed through because a container inherits none of the build host's network
configuration — behind a proxy or a regional firewall the default `proxy.golang.org` times out:

```bash
VERSION=$(git rev-parse --short HEAD)

docker build -f deploy/Dockerfile.agentd \
  --build-arg GOPROXY="$(go env GOPROXY 2>/dev/null || echo https://proxy.golang.org,direct)" \
  -t "$REG/agentd:$VERSION" .
docker build -f deploy/Dockerfile.console       -t "$REG/heros-console:$VERSION" .
docker build -f deploy/Dockerfile.admin-console -t "$REG/heros-admin-console:$VERSION" .

for r in agentd heros-console heros-admin-console; do docker push "$REG/$r:$VERSION"; done
```

Now capture the **digests** — this is what the manifests reference, never the tag:

```bash
for r in agentd heros-console heros-admin-console; do
  printf '%-22s %s\n' "$r" \
    "$(aws ecr describe-images --repository-name "$r" --image-ids imageTag="$VERSION" \
        --region "$AWS_REGION" --query 'imageDetails[0].imageDigest' --output text)"
done
```

Put those three digests into `deploy/k8s/base/kustomization.yaml` (the `images:` block) **and** into
`deploy/images.env`, changing the image names to your ECR registry. `make deploy-lint` fails if the two
substrates disagree or if anything is left on a floating tag — run it before you apply:

```bash
make deploy-lint
```

---

## 3. Create the EKS cluster — with NetworkPolicy actually enforced

```bash
eksctl create cluster \
  --name heros-prod --region "$AWS_REGION" \
  --version 1.31 \
  --nodegroup-name workers --node-type m6i.xlarge \
  --nodes 3 --nodes-min 3 --nodes-max 6 \
  --with-oidc \
  --managed
```

`--with-oidc` is required — §5's ambient identity depends on it.

🔴 **Then turn NetworkPolicy on.** Without this the policies apply and do nothing:

```bash
aws eks update-addon --cluster-name heros-prod --addon-name vpc-cni \
  --region "$AWS_REGION" \
  --configuration-values '{"enableNetworkPolicy":"true"}' \
  --resolve-conflicts OVERWRITE
```

(Alternatively install Calico or Cilium. What matters is that *something* enforces the objects.)

**Verify enforcement rather than assuming it** — this is the whole point of Decision 3, and a policy that
is present but inert looks identical to one that works:

```bash
kubectl -n heros run probe --rm -it --restart=Never --image=curlimages/curl -- \
  curl -m 5 http://postgres:5432
# EXPECTED: a timeout. A refused connection or a response means the policy is NOT being enforced.
```

Do this *after* §6's apply. If it does not time out, stop and fix the CNI before putting anything real
behind this cluster.

---

## 4. Storage: EBS CSI, and the AZ consequence

```bash
eksctl create addon --name aws-ebs-csi-driver --cluster heros-prod \
  --region "$AWS_REGION" --force
```

```yaml
# storageclass.yaml — gp3, encrypted, expandable. WaitForFirstConsumer matters: it makes the volume
# land in the AZ the pod was actually scheduled to, instead of pinning the pod to wherever the volume
# happened to be created.
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: heros-gp3
  annotations: { storageclass.kubernetes.io/is-default-class: "true" }
provisioner: ebs.csi.aws.com
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
parameters:
  type: gp3
  encrypted: "true"
```

⚠️ **`agentd` and `postgres` both use ReadWriteOnce claims, so each is pinned to one AZ.** An AZ outage
takes them down until the volume is restored elsewhere; this is the same single-point posture Decision 6
documents for Postgres, and it now applies to `agentd`'s ledger too (tenant credentials, registries,
memory, the tool index, the inbox). `agentd` also runs **one replica with `Recreate`**, so an upgrade is
briefly disruptive — see the README. Do not raise its replica count: two replicas over a single SQLite
ledger is two divergent databases, and on RWO the second will not even schedule.

---

## 5. Identity and secrets — no bootstrap secret, anywhere

Install the External Secrets Operator:

```bash
helm repo add external-secrets https://charts.external-secrets.io && helm repo update
helm install external-secrets external-secrets/external-secrets \
  -n external-secrets --create-namespace --wait
```

Create the secrets. **Every entry below is referenced by `deploy/k8s/base/externalsecrets.yaml`** — a
missing one leaves its pod waiting:

```bash
P=heros   # matches the remoteRef keys in externalsecrets.yaml

aws secretsmanager create-secret --name $P/postgres    --secret-string '{"password":"…"}'
aws secretsmanager create-secret --name $P/objectstore --secret-string '{"root-password":"…"}'
aws secretsmanager create-secret --name $P/graphstore  --secret-string '{"auth":"neo4j/…"}'

# The platform. `database-url` decides which database the schema is applied to; `config-json` is the
# TENANT CREDENTIAL SET both console BFFs authenticate with (a list, which is why it is a file).
aws secretsmanager create-secret --name $P/platform --secret-string '{
  "database-url":"postgres://heros@heros-prod.abc.us-east-1.rds.amazonaws.com:5432/heros?sslmode=require",
  "postgres-user":"heros",
  "postgres-password":"…",
  "config-json":"{\"auth_mode\":\"required\",\"tenant_credentials\":[{\"tenant_id\":\"acme\",\"api_key\":\"…\",\"role\":\"member\",\"key_id\":\"customer-console\"},{\"tenant_id\":\"acme\",\"api_key\":\"…\",\"role\":\"admin\",\"key_id\":\"operator-console\"}]}",
  "qdrant-api-key":"", "neo4j-password":"…", "inbox-signing-key":"",
  "openai-api-key":"", "anthropic-api-key":""
}'

aws secretsmanager create-secret --name $P/console --secret-string '{
  "platform-credential":"…", "tenant-assertions":"{\"…\":\"acme\"}",
  "idp-client-secret":"", "saml-sp-private-key":"", "idp-tenant-map":"", "idp-secret-map":""
}'

aws secretsmanager create-secret --name $P/admin-console --secret-string '{
  "platform-credential":"…", "admin-idp-client-secret":""
}'
```

🌐 **The provider secrets are a different shape, and you can defer them.** With
`HEROS_SECRETS_SOURCE=aws-secrets-manager` the gateway reads **one secret per provider**, named
`<HEROS_SECRETS_AWS_PREFIX><provider>`, whose value is the **raw key** — not a JSON field inside
`heros/platform`. Those `openai-api-key` / `anthropic-api-key` fields above feed the *other* path, the
`env` source the air-gapped overlay uses.

```bash
# Only when the model-calling stages ship — see §10. The secrets need not exist for the deploy to boot;
# the source is constructed at startup, a missing secret surfaces at the first model call, fail-closed.
aws secretsmanager create-secret --name heros/providers/openai    --secret-string 'sk-…'
aws secretsmanager create-secret --name heros/providers/anthropic --secret-string 'sk-ant-…'
```

What **is** required at boot is the pair that locates them — `HEROS_SECRETS_AWS_PREFIX` and
`HEROS_SECRETS_AWS_REGION` (§0 gap 5). They are set in the base; change the region if yours is not
`us-east-1`, in **both** the base and `overlays/prod/secretstore.yaml`.

🔴 The two `platform-credential` values must be **different**, and each must appear in `config-json`
with the matching role — `member` for the customer console, `admin` for the operator console. That is
what makes it true that neither console can act with the other's credential.

Now the IAM role and the **ServiceAccount the tree references but never defines** (gap 3):

The policy document goes in a file rather than inline. Two reasons, and the second is the one that bites:
a heredoc is auditable and diffable, and a policy substituted inline into `--attach-policy-arn` is created
by the **same command that attaches it**, so the step cannot be run twice — see below.

```bash
: "${ACCOUNT:?run the ACCOUNT/AWS_REGION exports from §2 first}" "${AWS_REGION:?}"

cat > /tmp/heros-secrets-read.json <<EOF
{ "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"],
    "Resource": "arn:aws:secretsmanager:$AWS_REGION:$ACCOUNT:secret:heros/*"
  }] }
EOF
```

🔴 **Look the policy up before creating it.** `create-policy` fails `EntityAlreadyExists` on the second
run — and in the shape this step used to have, that failure took the ARN substitution down with it, so
`eksctl` received an empty `--attach-policy-arn` and the whole step had to be unpicked by hand. Any
re-run reaches this: a cluster you recreated, an `eksctl` failure halfway, a second region:

```bash
SECRETS_POLICY_ARN=$(aws iam list-policies --scope Local \
  --query "Policies[?PolicyName=='HerosSecretsRead'].Arn" --output text)

[ -n "$SECRETS_POLICY_ARN" ] || SECRETS_POLICY_ARN=$(aws iam create-policy \
  --policy-name HerosSecretsRead \
  --policy-document file:///tmp/heros-secrets-read.json \
  --query Policy.Arn --output text)

echo "$SECRETS_POLICY_ARN"
```

⚠️ If it already existed, you are **reusing whatever it grants today**, which is not necessarily what the
file above says — a policy created against a different account or region has an ARN scope that silently
matches nothing. Confirm rather than assume, on any re-run:

```bash
aws iam get-policy-version --policy-arn "$SECRETS_POLICY_ARN" \
  --version-id "$(aws iam get-policy --policy-arn "$SECRETS_POLICY_ARN" \
      --query Policy.DefaultVersionId --output text)" \
  --query 'PolicyVersion.Document.Statement[0].Resource'
#   EXPECTED: arn:aws:secretsmanager:<your region>:<your account>:secret:heros/*
#   A different account or region here is why every ExternalSecret is stuck on AccessDenied.
```

Then the ServiceAccount:

```bash
eksctl create iamserviceaccount \
  --cluster heros-prod --region "$AWS_REGION" \
  --namespace heros --name heros-secrets \
  --attach-policy-arn "$SECRETS_POLICY_ARN" \
  --approve
```

The scope is `heros/*` and the verbs are read-only: this role can read these secrets and do nothing
else. There is no access key anywhere — the pod authenticates with its **ambient** identity (IRSA), which
is what makes "no bootstrap secret in the manifest" true rather than aspirational.

If your region is not `us-east-1`, change it in `deploy/k8s/overlays/prod/secretstore.yaml` — it is
hardcoded there.

---

## 6. Apply

```bash
kubectl apply -f storageclass.yaml
kubectl apply -k deploy/k8s/overlays/prod

kubectl -n heros get externalsecret        # every one must reach SecretSynced
kubectl -n heros rollout status deploy/agentd
kubectl -n heros get pods

# The Ingresses apply with this too (§7). An empty ADDRESS column is the controller telling you it
# could not provision an ALB — almost always the IAM role (§7.2) or the certificate ARN (§7.1).
kubectl -n heros get ingress
kubectl -n heros describe ingress console | tail -20   # the reason, when ADDRESS is empty
```

Applying twice is a no-op — that is the whole upgrade story. **Upgrade = push new images, update the
digests, apply again.** There is no teardown step and no migration command: `agentd` applies outstanding
migrations at boot and skips the ones its ledger already records.

Now run §3's enforcement probe.

---

## 7. Expose the two consoles

The manifests ship in **[`deploy/k8s/overlays/prod/ingress.yaml`](k8s/overlays/prod/ingress.yaml)** and are
already in the prod overlay's `resources:`, along with the two patches they depend on (§7.4). What is
*not* automatic is the controller that reconciles them and its IAM role — §7.2 — and two values you must
edit before applying: the **certificate ARN** and, if you are not deploying to `heros-agent.space`, the
**hostnames**. The target origins are:

| Surface | Origin |
|---|---|
| Customer console | `https://heros-agent.space` |
| Operator console | `https://admin.heros-agent.space` |
| Platform API (`agentd`) | **not exposed** — reachable only from the two consoles, by NetworkPolicy |

These are **two separate origins by design** (Decision 5: the isolation is the browser's origin
boundary and a disjoint cookie jar, not a route inside one app). Two hostnames, two Ingresses, two
ALBs. Do not merge them behind one host with a path split — that would hand the operator console's
cookies to the customer origin and undo the entire reason it is a separate deployment unit.

### 7.1 Certificate and DNS

One ACM certificate covers both, but **a wildcard alone will not do it**: `*.heros-agent.space` does
not match the apex `heros-agent.space`. Request both names:

```bash
aws acm request-certificate --region "$AWS_REGION" \
  --domain-name heros-agent.space \
  --subject-alternative-names '*.heros-agent.space' \
  --validation-method DNS
```

Complete the DNS validation, then note the ARN as `$CERT_ARN`.

DNS, once the ALBs exist (§7.2): the **apex must be an ALIAS A record, not a CNAME** — DNS forbids a
CNAME at a zone apex. The subdomain may be either; ALIAS is preferred, since it costs nothing to
resolve.

```
heros-agent.space          A     ALIAS -> <customer ALB dns name>
admin.heros-agent.space    A     ALIAS -> <operator ALB dns name>
```

### 7.2 The load balancer controller — and the IAM role it cannot work without

🔴 **The controller needs its own IAM role, and the Helm chart does not create one.** It calls
`elasticloadbalancing`, `ec2` and `acm` on your behalf; with no permissions it starts, goes `Running`,
logs `AccessDenied` where nobody is looking, and creates no ALB. `kubectl get ingress` shows an empty
`ADDRESS` column and that is the only outward sign. This is gap 2 in §0 and it is silent.

The policy document is published per controller release and **must match the chart you install** — a
newer chart calls actions an older policy does not grant. Derive the version instead of pinning one by
hand:

```bash
helm repo add eks https://aws.github.io/eks-charts && helm repo update

LBC_VERSION=$(helm show chart eks/aws-load-balancer-controller | awk '/^appVersion:/{print $2}')
echo "controller $LBC_VERSION"

curl -fsSL -o /tmp/lbc-iam-policy.json \
  "https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/${LBC_VERSION}/docs/install/iam_policy.json"
```

Create the policy — **idempotently**, because the second run of a `create-policy` fails
`EntityAlreadyExists` and takes the ARN substitution down with it:

```bash
LBC_POLICY_ARN=$(aws iam list-policies --scope Local \
  --query "Policies[?PolicyName=='AWSLoadBalancerControllerIAMPolicy'].Arn" --output text)

[ -n "$LBC_POLICY_ARN" ] || LBC_POLICY_ARN=$(aws iam create-policy \
  --policy-name AWSLoadBalancerControllerIAMPolicy \
  --policy-document file:///tmp/lbc-iam-policy.json \
  --query Policy.Arn --output text)

echo "$LBC_POLICY_ARN"
```

Bind it to a ServiceAccount in `kube-system` — the same IRSA mechanism as §5, so again **no access key
anywhere**:

```bash
eksctl create iamserviceaccount \
  --cluster heros-prod --region "$AWS_REGION" \
  --namespace kube-system --name aws-load-balancer-controller \
  --role-name AmazonEKSLoadBalancerControllerRole \
  --attach-policy-arn "$LBC_POLICY_ARN" \
  --approve
```

Then install the chart **against that ServiceAccount** — `serviceAccount.create=false` is the load-bearing
flag. Leave it at its default and Helm makes a second, unannotated ServiceAccount of the same name, the
pod authenticates as the node role instead, and you are back to `AccessDenied` with a role that looks
correctly configured in the console:

```bash
helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
  -n kube-system \
  --set clusterName=heros-prod \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller \
  --set region="$AWS_REGION" \
  --set vpcId="$(aws eks describe-cluster --name heros-prod --region "$AWS_REGION" \
      --query 'cluster.resourcesVpcConfig.vpcId' --output text)" \
  --wait
```

`region` and `vpcId` are passed explicitly rather than left to IMDS discovery: on a restricted-IMDS or
Fargate node the auto-detection fails at reconcile time, not at install time, which puts the error a long
way from the change that caused it.

**Verify the role is actually attached before you trust an empty `ADDRESS`** — a missing annotation and a
missing policy produce the same symptom:

```bash
kubectl -n kube-system get sa aws-load-balancer-controller \
  -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}{"\n"}'
#   EXPECTED: the AmazonEKSLoadBalancerControllerRole ARN. Empty means the chart created its own
#   ServiceAccount over yours — uninstall, re-run eksctl, reinstall with serviceAccount.create=false.
```

### 7.2.1 The two Ingresses

They ship in [`overlays/prod/ingress.yaml`](k8s/overlays/prod/ingress.yaml), already listed in the prod
overlay's `resources:`. Read the file — its comments carry the reasoning that used to live here. What you
must change in it, and nothing else:

| Edit | Why it cannot be defaulted |
|---|---|
| `certificate-arn` ×2 → `$CERT_ARN` from §7.1 | The committed placeholder is a **valid-looking ARN that does not exist**, so the controller refuses and the Ingress never gets an address. Deliberate: a missing certificate must fail in `kubectl describe ingress`, not quietly serve plain HTTP. |
| `host` ×2, if not `heros-agent.space` | Concrete hostnames, not empty ones — **an Ingress rule with a blank host matches every host that reaches the ALB**, which would serve the operator console from the customer hostname. A wrong hostname is inert; a blank one is a boundary failure. |
| `ipBlock.cidr` in the `agentd` NetworkPolicy patch (in `kustomization.yaml`) | §7.4 — it is your VPC's CIDR, and `10.0.0.0/16` is only eksctl's default. |

Render before you apply; the whole point of kustomize here is that what you read is what applies:

```bash
kustomize build deploy/k8s/overlays/prod | grep -A3 'certificate-arn'
```

🔴 **Two Ingresses, and therefore two ALBs — not one ALB with two host rules.** The
`alb.ingress.kubernetes.io/group.name` annotation would merge them onto shared infrastructure; it is
absent from both files and should stay absent. A shared ALB means one WAF association, one access-log
stream and one set of listener rules for both the customer surface and the cross-tenant operator
surface, and every future change to either is a change that can reach the other.

⚠️ The base `NetworkPolicy` allows ingress to the consoles on their ports from anywhere in the cluster,
which covers ALB targets. The platform API (`agentd`, 4321) accepts ingress **only from the two consoles**
and is deliberately not exposed — **do not add a blanket Ingress for it.** The two paths that are the real
question are the CLI's; see §7.4.

### 7.3 The hostname-derived configuration

Six values are derived from the two hostnames. They are not cosmetic — four of them are security
boundaries, and a wrong one either breaks sign-in or quietly widens a boundary. Patch them in your
overlay:

```yaml
# On agentd
- { name: ADMIN_CONSOLE_ORIGIN,  value: "https://admin.heros-agent.space" }
- { name: ADMIN_WEBAUTHN_RP_ID,  value: "admin.heros-agent.space" }
- { name: ADMIN_IDENTITY_MODE,   value: "configured" }

# On the operator console
- { name: ADMIN_IDP_CALLBACK_URL, value: "https://admin.heros-agent.space/auth/callback" }

# On the customer console — `oidc` is the primary mechanism; leave it `configured` to federate with
# nobody, or set `saml` and fill the SAML pair instead.
- { name: CONSOLE_TENANT_IDENTITY,        value: "oidc" }
- { name: CONSOLE_IDP_REDIRECT_ALLOWLIST, value: '["https://heros-agent.space/auth/callback"]' }
# SAML only:
- { name: CONSOLE_SAML_ACS_ALLOWLIST,     value: '["https://heros-agent.space/auth/saml/acs"]' }
```

🔴 **`ADMIN_WEBAUTHN_RP_ID` must be `admin.heros-agent.space`, not `heros-agent.space`.** A WebAuthn
Relying Party ID may be the origin's domain *or any registrable suffix of it*, so setting it to the
apex would make operator security keys valid across **every** `*.heros-agent.space` origin — including
the customer console. `internal/adminidentity/mfa.go` puts it plainly: origin-binding is the one thing
WebAuthn gives over TOTP, and a loose comparison gives it back. Scope it to the operator subdomain and
the credential cannot be replayed anywhere else.

`ADMIN_CONSOLE_ORIGIN` is an **exact-match** origin allowlist, not a suffix rule — full scheme and host,
no trailing slash. Same for `CONSOLE_IDP_REDIRECT_ALLOWLIST`, whose first entry is the callback this
deployment actually sends; a wildcard in it is **refused at load**, because an allowlist with a wildcard
is not an allowlist.

Register both callback URLs with your IdP before cutting DNS over. Then, **before `admin.heros-agent.space`
resolves publicly**, confirm the two things that make an internet-facing operator console defensible:

```bash
curl -s http://127.0.0.1:4310/api/health | jq -r .identity_mode
#   EXPECTED: oidc.  `dev` must refuse to start under NODE_ENV=production — if you see it running in
#   dev mode, stop and fix that before the DNS record exists.
#   🔴 `configured` here is ALSO a failure, and a quiet one: isFederated() accepts only
#   oidc/saml, so /auth/login 303s to /signin?reason=not_federated and no operator can ever get past
#   the sign-in page. It looks like a working console right up until somebody tries to use it.

curl -s http://127.0.0.1:4321/readyz | jq '.admin_idp'
#   EXPECTED: the real operator IdP — {"kind":"oidc","issuer":"https://<org>.okta.com/oauth2/default"}.
#   `null` means agentd is not serving an operator identity at all. Check ADMIN_IDENTITY_MODE on the
#   AGENTD pod: it must be `oidc`, not `configured`, and empty means no admin API was built.

curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:4311/admin/api/healthz
#   EXPECTED: 200. This is the operator admin API on its own listener. A connection refused means
#   ADMIN_IDENTITY_MODE was not federated at boot, so nothing was mounted — which the console reports
#   only as a rejected sign-in.
```

---

### 7.4 The CLI link path — two Ingress rules and the two patches they need

The `heros` CLI (`login` and `link`) transmits to a **hardcoded, allowlist-enforced constant**:

```go
// internal/runlink/allowlist.go
const PlatformBaseURL = "https://heros-agent.space"
const LinkPath        = "/api/v1/run-links"
```

`assertLinkTarget` refuses any other host — deliberately, so a compromised config cannot redirect a run
payload elsewhere. It calls `GET /api/v1/whoami` to validate the token, then POSTs the run to
`/api/v1/run-links`, both with `Authorization: Bearer <token>`.

**Those are platform routes on `agentd:4321`, served under your customer-console hostname.** The console
BFF serves `consent`, `console/*`, `health`, `session`, `stream` and `theme` — nothing under `/api/v1`.
So without the rule below, `heros login` reaches Next.js and gets a **404 that looks like a networking
problem and is not one**.

Exactly two paths are on the customer Ingress — **not** a blanket rule for `/api/v1/*`. The rest of 4321
is a console-only surface reached with the BFF's credential; these two are a bearer-token surface built
for machines, and they are the only ones a developer's laptop should be able to reach. They are listed
**before** the catch-all `/` rule, because the controller emits listener rules in the order they appear
and a `Prefix` rule on `/` above them would swallow both.

Two patches in `overlays/prod/kustomization.yaml` make them work. Both are already there; both are the
kind of thing whose absence produces a 503 with no explanation.

**(a) Let the load balancer reach `agentd` at all.** The base `NetworkPolicy` admits 4321 only from the
two consoles, and with `target-type: ip` the ALB is not a pod — it arrives from the VPC:

```yaml
  - target: { kind: NetworkPolicy, name: agentd }
    patch: |
      # …ingress from the two consoles, plus:
              - ipBlock: { cidr: 10.0.0.0/16 }   # 🔴 YOUR VPC CIDR, not 0.0.0.0/0
```

```bash
aws ec2 describe-vpcs --filters Name=tag:Name,Values='eksctl-heros-prod-cluster/VPC' \
  --query 'Vpcs[0].CidrBlock' --output text
```

**(b) Give `agentd`'s target group its own health check.** 🔴 The Ingress-level
`healthcheck-path: /api/health` is right for the console and **wrong for `agentd`, which serves
`/healthz` and `/readyz` and nothing at `/api/health`.** Without an override the two rules above route
correctly, the target deregisters as unhealthy, and the CLI gets a 503 whose cause is nowhere near the
rule that looks responsible. The controller reads health-check annotations from the **Service** when
they need to differ per target group:

```yaml
  - target: { kind: Service, name: agentd }
    patch: |
      metadata:
        annotations:
          alb.ingress.kubernetes.io/healthcheck-path: /healthz
```

`/healthz` and not `/readyz`, deliberately: `/readyz` goes non-200 when *any* aggregated component is
degraded, so pointing a load balancer at it would deregister the only `agentd` pod — and break the CLI
link path — because, say, the vector store is unreachable. Liveness for the load balancer, readiness for
the kubelet.

⚠️ **Be honest with yourself about what that policy does.** A NetworkPolicy matches L3/L4 — addresses and
ports, never paths. Admitting the ALB to 4321 admits it to **all** of 4321; the restriction to those two
paths is enforced by the Ingress rules above and by nothing else. So keep the Ingress's path list exact
(`pathType: Exact`, not `Prefix`), and treat any future `/api/v1/*` rule on the public hostname as a
security review rather than a routing tweak.

**The store side is done.** P11 previously could not mount at all — its only `Store` was in-memory, so
accepting a linked run and forgetting it on restart was the alternative to 503. It now has a Postgres
store (migration `0020_p11_run_links`) and mounts for real.

There is no separate readiness component for it, and that is deliberate: `linkingest.Store` returns an
error on every method, so a failed read fails its **caller** rather than being reported somewhere else
and hoped for. Coverage that cannot be read is rendered UNKNOWN — never zero, never complete — and the
`postgres` component already reports the database itself. One signal per dependency; two that can
disagree is worse than one.

*(The CLI's bearer token has nothing to do with the browser SSO in §7.3 — two principals, two surfaces.
See A.6.)*

---

## 8. Verify — the same four checks the single-host path runs

```bash
kubectl -n heros port-forward svc/agentd 4321:4321 &

# 1. The aggregated verdict. `ready` only when every wired component answers; it NAMES a degraded one.
curl -s http://127.0.0.1:4321/readyz | jq

# 2. What this deployment actually serves — the table, from the boot log.
kubectl -n heros logs deploy/agentd | grep -E 'served|not mounted'

# 3. Auth is ENFORCED, not merely configured. "config-json did not load" and "auth is off" look
#    identical from outside, and only one of them is intended.
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:4321/api/v1/prompts
#    EXPECTED: 401. A 200 means config-json did not reach the pod.

# 4. The schema. A second apply must change nothing.
kubectl -n heros logs deploy/agentd | grep 'platform schema'
#    EXPECTED on a re-deploy: "already current (N migrations)".
```

Then the two public surfaces, from outside the cluster:

```bash
# Both must serve over HTTPS, and both must redirect plain HTTP rather than answering on it.
curl -sI https://heros-agent.space/            | head -1
curl -sI https://admin.heros-agent.space/      | head -1
curl -sI http://heros-agent.space/             | head -1   # EXPECTED: 301 to https
curl -sI http://admin.heros-agent.space/       | head -1   # EXPECTED: 301 to https

# 🔴 The origin boundary, asserted rather than assumed. The operator console must NOT be reachable
# from the customer hostname — no path, no rewrite, no shared ALB rule.
curl -s -o /dev/null -w '%{http_code}\n' https://heros-agent.space/app/admin
#    EXPECTED: 404 from the customer console. Anything that renders an operator surface here means the
#    two Ingresses have been merged, and the isolation Decision 5 rests on is gone.

# The platform API must not be exposed on either hostname.
curl -s -o /dev/null -w '%{http_code}\n' https://heros-agent.space/readyz
#    EXPECTED: 404 — /readyz belongs to agentd, which no Ingress routes to.
```

Then sign in to both, and confirm the operator sign-in actually demands MFA — not that the page offers
it, but that a session cannot be obtained without completing it.

---

## 9. Operate

**Consent retention** ships as a weekly `CronJob` that **refuses by default** — no window set, and no
`-apply`. Retention is a legal answer, not an engineering default. Once counsel states the period, patch
it in your overlay (`61320h` is seven years), run it once by hand, and read the output before letting the
schedule act:

```bash
kubectl -n heros create job --from=cronjob/legal-retention retention-manual
kubectl -n heros logs job/retention-manual
```

**Back up both stores.** The Postgres dump (or RDS automated backups) is not enough: `agentd`'s ledger
holds every tenant credential and lives on its own EBS volume. Snapshot it on the same schedule.

**Rollback = re-apply the prior digests.** The same command with the older image digests. It is
non-destructive to data produced during the upgrade window, and it never falls back to a same-version
backup.

---

## 10. What you are *not* getting yet

Stated plainly so it is not discovered in a demo. Per the README's capability table, a fresh install
**serves** the health endpoints, P13 coverage/delivery, P17 memory, P20 install, and — with a platform
database — the P10 prompt registry, the studio matrix's model/render half, the P2 read views, and P23
consent. Everything else is **registered and answers 503 not-mounted**: the eval board, scorecard, graph
editor, proposals, optimizer, pattern graph and run monitor have no persistent adapter yet; billing,
payments, run-linking and authoring have only in-memory stores, so mounting them would take data and
forget it. That is deliberate, it is visible in the boot log, and it is the honest state — not a
misconfiguration on your side.

Budget accordingly: an EKS cluster serving mostly-503 capabilities is a real cost. §1(b) is where you
decide how much of it to pay for today.

---

## Appendix A — every environment variable

The complete contract, extracted from `deploy/k8s/base/` — **58 declarations across three workloads, 51
distinct names** (`HEROS_VERSION`, `HEROS_EDITION`, `NODE_ENV`, `HEROS_SECRETS_SOURCE` and
`ADMIN_IDENTITY_MODE` each appear on more than one workload, deliberately and with the same meaning).

Docker Compose declares the same set. `make deploy-lint` fails the build if the two substrates ever
diverge on it — which is how `ADMIN_CONSOLE_HEALTH_URL`, seven identity variables and both
provider-credential names were found present on one side and missing from the other.

Legend:

| Mark | Meaning |
|---|---|
| **🌐** | **The value comes from a THIRD PARTY.** You cannot generate it — someone outside this system issues it, and you have to go and get it. **A.0 is the shopping list.** |
| **🔑** | You generate it yourself (a random secret, or a name you choose). No one to ask. |
| **☁️** | AWS issues it — an ARN, an endpoint, a role. Self-service in your own account. |
| **✏️** | You must set it for `heros-agent.space` specifically; the shipped value is a placeholder. |
| **🔐** | Delivered from Secrets Manager by the External Secrets Operator — never written into a manifest. |
| *optional* | Absent is a legitimate, supported state. |

🌐 and 🔐 are orthogonal: a third-party value still arrives through Secrets Manager. 🌐 is about **who
you have to ask**; 🔐 is about **how it reaches the pod**.

### A.0 What you must obtain from a third party 🌐

Everything else in this appendix you can produce yourself. These you cannot — collect them before you
start, because each one blocks a sign-in flow you cannot test without it.

**1 · From your identity provider — TWO separate app registrations**

Two, not one, and deliberately so: the operator identity domain is disjoint from the customer's at every
layer, and its credentials are the last place to blur that. (P22 was validated live against Okta.)

| You need from them | Lands in | For |
|---|---|---|
| Issuer URL | `CONSOLE_IDP_ISSUER` | customer sign-in |
| Client ID | `CONSOLE_IDP_CLIENT_ID` | customer sign-in |
| Client secret | 🔐 `heros/console` → `idp-client-secret` | customer sign-in |
| Issuer URL (operator app) | `ADMIN_IDP_ISSUER` | operator sign-in |
| Client secret (operator app) | 🔐 `heros/admin-console` → `admin-idp-client-secret` | operator sign-in |

**Yes, the customer console really does federate** — this is not operator-only plumbing. `web/console`
ships three live routes that the `CONSOLE_IDP_*` / `CONSOLE_SAML_*` variables drive:

| Route | Purpose |
|---|---|
| `/auth/login` | starts the OIDC flow |
| `/auth/callback` | receives the OIDC redirect |
| `/auth/saml/acs` | the SAML assertion consumer service |

plus a `/signin` page that renders whichever mechanism `CONSOLE_TENANT_IDENTITY` selects. On
`configured` the console federates with nobody and uses the assertion→tenant map instead — which is why
the base ships `configured` and why every IdP variable is optional. Set it to `oidc` or `saml` and the
matching variables become required.

You give **them** these, in return:

- customer redirect URI — `https://heros-agent.space/auth/callback`
- customer SAML ACS URL — `https://heros-agent.space/auth/saml/acs` *(SAML only)*
- operator callback URI — `https://admin.heros-agent.space/auth/callback`

⚠️ The redirect allowlist is normalised to **origin + pathname** and compared by **string equality** at
the callback. A trailing slash, `http` instead of `https`, or a query string makes it a different URL and
the sign-in is refused. That is deliberate — `config.ts` rejects a `*` outright, because a wildcard
redirect target is an open redirect by construction.

⚠️ **Three things that bite, in this order** (learned from the live Okta run, and they fail in sequence —
fixing one reveals the next):

1. **The redirect URI must match exactly**, including scheme and trailing path.
2. **A custom authorization server ships with ZERO access policies.** Token requests against one fail
   until you add a policy — and the failure does not say "no policy", so it reads as a credential
   problem for as long as you let it.
3. **The sign-in widget** is configured separately from the app; a correct app with an unconfigured
   widget still cannot complete a sign-in.

**If you use SAML instead of OIDC**, you need from them: `CONSOLE_SAML_IDP_ENTITY_ID` and
`CONSOLE_SAML_IDP_METADATA_URL`. You supply *them* your `CONSOLE_SAML_SP_ENTITY_ID` (a name you choose)
and the SP certificate derived from `CONSOLE_SAML_SP_PRIVATE_KEY` (🔑 you generate that key).

**2 · From your operators — the IdP subject, and MFA enrolment**

Not variables, but they gate the operator console and there is no way to configure around either.

You need each operator's **`sub` claim** — the IdP's own opaque user id, which Okta issues as `00u…`.
Not their email: the subject is stable and an email is a mutable attribute, so binding an operator to an
address means a rename at the IdP silently moves or destroys their access. The bootstrap command refuses
a value containing `@` for that reason.

Operator MFA is an **invariant**, not a setting. With WebAuthn, `ADMIN_WEBAUTHN_RP_ID` must be
`admin.heros-agent.space` **before** anyone enrols — credentials are bound to the RP ID, so changing it
later invalidates every key already registered.

**2a · Creating the first operator — the bootstrap, and why it is two passes**

🔴 **A fresh deployment has nobody who can sign in, and the console cannot fix that itself.** The platform
will not issue a session without a platform-verified second factor; enrolling a factor requires a session;
at install time nobody has either. That deadlock is deliberate — `internal/api/identityflow.go` calls it a
two-person operation by design — and `agentd -admin-bootstrap-subject` is the other person's tool. It runs
as its own process (a Job, a one-off `docker run`), against the platform database, and does not need
`agentd` to be running.

```bash
agentd -admin-bootstrap-subject=00u15tilol4I6bR3n698 -admin-bootstrap-role=superadmin
```

Run it **twice**, and the split is the check rather than an inconvenience:

- **Pass 1** — the TOTP seed is not in the secrets manager yet. The command generates a candidate, prints
  the `otpauth://` URI to add to the operator's own phone and the logical name to store it under, and
  **writes nothing**. A half-done bootstrap leaves no directory row behind.
- **Pass 2** — the seed resolves. The command reads it back **through the same secrets seam a sign-in
  uses**, checks it decodes the way the verifier will decode it, and only then writes the principal, the
  role grant and the factor index.

That read is the point. It converts "the secret was stored under the wrong name, in the wrong region, or
under a policy this role cannot read" from a sign-in that fails at the factor step with a message about
the factor, into a bootstrap that fails while the person who can fix it is looking at the terminal.

The seed is stored the way every credential in this source is stored: as the JSON object
`{"api_key": "<seed>"}`, not as a bare string — a bare string is rejected as malformed, and pass 2 is
what catches that before an operator ever sees it. The seed itself is **never** written to the platform
database — `admin_factor` stores only the logical name it is held under. It is also never written to the process log: it is printed once, to the terminal,
because a log is shipped, indexed and retained, and a seed in it is a second factor anybody with log
access holds.

⚠️ **Restart `agentd` after pass 2.** The directory is read at start-up, so a process that was already
running does not yet know the operator exists.

⚠️ The command is safe to re-run: it reconciles rather than duplicating. It **refuses** to rebind an
existing `admin_id` to a different subject, because with a wrong subject that is an account takeover.

**3 · From your model provider(s) — and NOT YET**

| You need from them | Lands in |
|---|---|
| OpenAI API key | AWS secret `heros/providers/openai` |
| Anthropic API key | AWS secret `heros/providers/anthropic` |

🔴 **You do not need these to deploy.** Every model-calling stage — the eval board, scorecard, proposals
and the optimizer — is **registered but not mounted** on this release (§10), so nothing calls a provider.
What you *do* need at boot is `HEROS_SECRETS_AWS_PREFIX` and `HEROS_SECRETS_AWS_REGION` set, because the
secrets **source** is constructed at startup; the secrets themselves are looked up lazily, at the first
model call, and are allowed not to exist yet. Create them when those stages ship.

Note the shape: with `HEROS_SECRETS_SOURCE=aws-secrets-manager` the gateway reads **one AWS secret per
provider**, named `<prefix><provider>`, whose value is the raw key. That is a different path from the
`openai-api-key` / `anthropic-api-key` fields in `heros/platform`, which feed the `env` source used by
the air-gapped overlay. Both are listed in A.1; only one is live on a given deployment.

**4 · Not third-party** — for contrast, so you do not go looking: every database password, object-store
password, `NEO4J_AUTH`, both `platform-credential` values, `tenant-assertions`, `config-json` and the
SAML SP private key are 🔑 **yours to generate**. The RDS endpoint, ACM certificate ARN and IAM role ARN
are ☁️ **AWS's**, from your own account.

### A.1 `agentd` — the platform service (24)

| Variable | Source | Value on AWS | What it does / what goes wrong |
|---|---|---|---|
| `HEROS_LISTEN_ADDR` | literal | `0.0.0.0:4321` | A container that binds `127.0.0.1` is unreachable from a sibling pod. |
| `HEROS_DATA_DIR` | literal | `/var/lib/heros` | The SQLite ledger's mount point — **user state**, on the `agentd-ledger` PVC. |
| `HEROS_CONFIG` | literal | `/etc/heros/config.json` | Projected from 🔐 `heros-platform/config-json`. Carries the **tenant credential set**; a list, which is why it is a file and not a variable. |
| `DATABASE_URL` | ☁️ 🔐 required | `heros-platform/database-url` | Decides which database the schema is applied to. Unreachable ⇒ **the boot fails**, deliberately. Unset ⇒ the Postgres-backed capabilities register unsourced and report 503. |
| `HEROS_SECRETS_SOURCE` | literal | `aws-secrets-manager` | The seam the platform's own model calls resolve credentials through. `env` \| `aws-secrets-manager` \| `file`. Reported on `/readyz`. |
| `HEROS_SECRETS_AWS_REGION` | literal | ✏️ `us-east-1` | 🔴 **Required** when the source is `aws-secrets-manager`, and resolved **at boot** — Secrets Manager endpoints are per-region. Falls back to `AWS_REGION`. Keep it equal to the region in `overlays/prod/secretstore.yaml`. |
| `HEROS_SECRETS_AWS_PREFIX` | literal | `heros/providers/` | 🔴 **Required** (or `HEROS_SECRETS_AWS_IDS`) — without one the process exits at boot and the pod CrashLoopBackOffs. Expands to one secret per supported provider (`heros/providers/openai`, …), so adding a provider needs no manifest edit. The secrets may not exist yet: the map is built at boot, a missing one surfaces at the first model call. |
| `OPENAI_API_KEY` | 🌐 🔐 optional | `heros-platform/openai-api-key` | Read **only** when the source is `env` (the airgapped overlay). Declared here so switching the source is a one-line change, not a debugging session. |
| `ANTHROPIC_API_KEY` | 🌐 🔐 optional | `heros-platform/anthropic-api-key` | As above. |
| `HEROS_VERSION` | literal | ✏️ your release tag | Build identity on reports and the release surface. |
| `HEROS_EDITION` | literal | `kubernetes` | 🔴 A **deployment SHAPE**, not a commercial tier. Closed set, enforced in `internal/erroreport`: `hosted` \| `compose` \| `kubernetes` \| `airgapped` \| `dev`. Anything else — including the `open-core`/`managed`/`enterprise` names this table used to carry — logs `error_reporting.edition.unrecognised` and reports as `unknown`, which makes the one dimension you group errors by useless. `kubernetes` is right for this runbook; `hosted` is reserved for the platform's own deployment, which is configured out of band (§A.4). |
| `QDRANT_API_KEY` | 🔑 🔐 optional | `heros-platform/qdrant-api-key` | Unused today — the vector store is *provisioned ahead of use* (§1b). |
| `NEO4J_PASSWORD` | 🔑 🔐 optional | `heros-platform/neo4j-password` | Unused today — graph store, same. |
| `HEROS_INBOX_SIGNING_KEY` | 🔑 🔐 optional | `heros-platform/inbox-signing-key` | Inbox signature verification. Unset ⇒ no inbox on this deployment. |
| `ADMIN_IDENTITY_MODE` | literal | ✏️ `oidc` | 🔴 **`test` \| `oidc` \| `saml` — NOT `configured`.** This is the *platform's* selector (`adminidentity.ProviderFromEnv`); `configured` belongs to the console and is refused here. Empty ⇒ no admin API is served at all. |
| `ADMIN_IDP_ISSUER` | 🌐 literal | ✏️ your operator IdP issuer | The OIDC issuer, e.g. `https://<org>.okta.com/oauth2/default`. Required once federated. |
| `ADMIN_IDP_CLIENT_ID` | 🌐 literal | ✏️ your operator app's client id | Required once federated. |
| `ADMIN_IDP_REDIRECTS` | 🌐 literal | ✏️ `["https://admin.heros-agent.space/auth/callback"]` | JSON array of **exact** callback URIs. Must match `ADMIN_IDP_CALLBACK_URL` and the IdP's registered redirect URI character for character. A wildcard is refused at load. |
| `ADMIN_API_LISTEN_ADDR` | literal | `0.0.0.0:4311` | The operator admin API's **own** listener (P8 Decision 11). 🔴 Never publish it through an Ingress — its only caller is the admin BFF, in-cluster. |
| `ADMIN_CONSOLE_ORIGIN` | literal | ✏️ `https://admin.heros-agent.space` | **Exact-match** WebAuthn origin allowlist — full scheme + host, no trailing slash, not a suffix rule. |
| `ADMIN_WEBAUTHN_RP_ID` | literal | ✏️ `admin.heros-agent.space` | 🔴 The Relying Party ID. Setting it to the apex makes operator keys valid on **every** `*.heros-agent.space` origin — see §7.3. |
| `CONSOLE_HEALTH_URL` | literal | `http://console:4320/api/health` | Aggregates the customer console **and** the customer IdP into `/readyz` as two separately-named components. Also the origin `agentd` derives the legal-manifest URL from. |
| `ADMIN_CONSOLE_HEALTH_URL` | literal | `http://admin-console:4310/api/health` | Aggregates the operator console. Unset ⇒ readiness reports green while the operator surface is dead. |
| `OBJECT_STORE_HEALTH_URL` | literal | `http://objectstore:9000/…` | Drop it if you drop the store (§1b) — a component with no URL is *honestly absent*; one with a URL that is unreachable turns `/readyz` not-ready and names itself. |
| `QUEUE_HEALTH_URL` | literal | `http://queue:8222/healthz` | As above. |
| `VECTOR_STORE_HEALTH_URL` | literal | `http://vectorstore:6333/readyz` | As above. |
| `GRAPH_STORE_HEALTH_URL` | literal | `http://graphstore:7474/` | As above. |

There is one more `agentd` reads that is **not** in the manifests: `HEROS_SECRETS_HEALTH_URL`, set only
by the airgapped overlay, which aggregates the on-prem gateway's reachability and makes the model stages
report degraded-not-available when it is down.

### A.2 `console` — the customer BFF on `heros-agent.space` (27)

| Variable | Source | Value on AWS | What it does / what goes wrong |
|---|---|---|---|
| `PLATFORM_API_BASE` | literal | `http://agentd:4321` | In-cluster only. The browser never reaches the platform API. |
| `NODE_ENV` | literal | `production` | Also what makes a `dev` identity seam refuse to start. |
| `CONSOLE_TENANT_IDENTITY` | literal | ✏️ `oidc` | `configured` (federates with nobody) \| `oidc` \| `saml` \| `dev`. Base ships `configured` so open-core is not the exception. |
| `CONSOLE_PLATFORM_CREDENTIAL` | 🔑 🔐 required | `heros-console/platform-credential` | The BFF's **own** key. Must appear in `config-json` with role `member`. Never reaches the browser. |
| `CONSOLE_TENANT_ASSERTIONS` | 🔑 🔐 required | `heros-console/tenant-assertions` | JSON assertion→tenant map for the `configured` seam. |
| `CONSOLE_IDP_ISSUER` | 🌐 literal | ✏️ your IdP issuer | |
| `CONSOLE_IDP_CLIENT_ID` | 🌐 literal | ✏️ your client id | |
| `CONSOLE_IDP_CLIENT_SECRET` | 🌐 🔐 optional | `heros-console/idp-client-secret` | 🔴 An identity secret **mints identities** — worse to leak than a provider key, which only spends money. |
| `CONSOLE_IDP_REDIRECT_ALLOWLIST` | literal | ✏️ `["https://heros-agent.space/auth/callback"]` | JSON array; the first entry is the one sent. **A wildcard is refused at load.** |
| `CONSOLE_IDP_TENANT_MAP` | 🔑 🔐 optional | `heros-console/idp-tenant-map` | Strategy + issuer registrations. `verified_domains` hangs off the **issuer**, which is what stops IdP A claiming tenant B's domain. |
| `CONSOLE_IDP_SECRET_MAP` | 🔑 🔐 optional | `heros-console/idp-secret-map` | Issuer → client-secret resolution. |
| `CONSOLE_IDP_TIMEOUT_MS` | literal | `5000` | An unbounded IdP call turns a slow provider into a hung sign-in page. |
| `CONSOLE_SAML_IDP_ENTITY_ID` | 🌐 literal | ✏️ if using SAML | The enterprise alternative to OIDC. |
| `CONSOLE_SAML_SP_ENTITY_ID` | 🔑 literal | ✏️ if using SAML | |
| `CONSOLE_SAML_IDP_METADATA_URL` | 🌐 literal | ✏️ if using SAML | |
| `CONSOLE_SAML_ACS_ALLOWLIST` | literal | ✏️ if using SAML | Same no-wildcard rule as the OIDC allowlist. |
| `CONSOLE_SAML_SP_PRIVATE_KEY` | 🔑 🔐 optional | `heros-console/saml-sp-private-key` | 🔴 Mints identities. |
| `CONSOLE_SESSION_TTL_SECONDS` | literal | `28800` | 8h. |
| `CONSOLE_UPSTREAM_TIMEOUT_MS` | literal | `10000` | Bounds BFF→API. Unbounded means a hung tab with no error to read. |
| `HEROS_SECRETS_SOURCE` | literal | `env` | `env` here because ESO has already materialised the values into the pod. The airgapped overlay uses `file`. |
| `HEROS_SECRETS_DIR` | literal | `/var/run/secrets/heros` | Where `file`-sourced credentials are read **at the moment of use**, so a rotated file lands on the next sign-in with no restart. |
| `CONSOLE_CONSENT_GATE` | literal | ✏️ per your legal posture | P23 consent gating. |
| `HEROS_CONTENT_ROOT` | literal | *(empty — in-image default)* | The published legal/docs corpus root. The corpus ships **in the image** (ADR-011). |
| `HEROS_VERSION` | literal | ✏️ your release tag | |
| `HEROS_EDITION` | literal | `kubernetes` | Deployment shape, closed set — see §A.2. |
| `HEROS_SLUG_MANIFEST` | literal | *(empty)* | P20 install/download surface. |
| `HEROS_RELEASE_OFFLINE` | literal | *(empty on AWS)* | Set only in the airgapped overlay — a download page pointing at GitHub from a network with no egress shows a dead link and no reason. |

### A.3 `admin-console` — the operator BFF on `admin.heros-agent.space` (7)

| Variable | Source | Value on AWS | What it does / what goes wrong |
|---|---|---|---|
| `ADMIN_API_BASE` | literal | `http://agentd:4311` | In-cluster only. 🔴 **4311, not 4321.** The customer API on 4321 serves no `/admin/api/*` route — the admin surface is a separate handler on a separate listener. Pointed at 4321 every admin call 404s, and the BFF turns that into one generic "that sign-in was not accepted". |
| `NODE_ENV` | literal | `production` | What makes `ADMIN_IDENTITY_MODE=dev` refuse to start. |
| `ADMIN_IDENTITY_MODE` | literal | ✏️ `oidc` | 🔴 **Not `configured`.** `isFederated()` accepts only `oidc`/`saml`; with `configured`, `GET /auth/login` 303s to `/signin?reason=not_federated` and the operator can never leave the sign-in page. Verify it in the running pod **before** the public DNS record exists (§7.3). |
| `ADMIN_PLATFORM_CREDENTIAL` | 🔑 🔐 required | `heros-admin-console/platform-credential` | **Distinct** from the customer BFF's, and must appear in `config-json` with role `admin`. Neither console can act with the other's credential. |
| `ADMIN_IDP_CALLBACK_URL` | literal | ✏️ `https://admin.heros-agent.space/auth/callback` | Must be on the **operator** origin. Pointing it at the customer origin lands an operator's assertion in the customer console's cookie jar. |
| `HEROS_VERSION` | literal | ✏️ your release tag | |
| `HEROS_EDITION` | literal | `kubernetes` | Deployment shape, closed set — see §A.2. |

### A.4 Deliberately absent, and why

**The P24 analytics and error-reporting switches** — `HEROS_ERROR_REPORTING_DSN`, `HEROS_GA4_MEASUREMENT_ID`,
`HEROS_GA4_API_SECRET`, `HEROS_CLARITY_PROJECT_ID`, `HEROS_SOURCEMAP_UPLOAD_TOKEN` — **appear in no
manifest and no `.env` example, not even empty**, and a build fence (`internal/deploy`) fails if one is
added. An empty slot is one `--set` from being filled in a file a customer edits without reading, and a
default nobody chose takes effect the day somebody's shell exports the variable. Those integrations are
configured for the platform's own hosted deployment, from its own environment. A deployment that reports
to nobody is the **correct** state: `/readyz` says `absent` and stays silent about it.

⚠️ One consequence worth knowing: `HEROS_ERROR_REPORTING_DSN` **set and unusable is a hard boot
failure**, by design. If you configure it out-of-band on your hosted deployment, get it right — the
alternative the authors rejected was a process that starts, looks healthy, and reports nothing for a month.

**Not runtime variables:** `AGENTD_PORT` / `CONSOLE_PORT` / `ADMIN_CONSOLE_PORT` are Compose host-port
publishing only (Kubernetes uses Services). `GOPROXY` is a **build arg** for `Dockerfile.agentd` (§2),
not a runtime value. `HEROS_TEST_POSTGRES_URL`, `ADMIN_DEMO_SSO_SUBJECT` and `ADMIN_CONSOLE_URL` are
test and fixture inputs — none belongs in a production manifest.

### A.5 Stripe and Sentry — where they actually are

Two integrations you would expect to find above, and do not. Neither is an oversight.

**Stripe has no environment variable at all — by design.** Billing credentials are not env vars in any
substrate. They are two **reserved logical names** resolved through the *same*
`providergateway.Secrets` source as the model credentials, at the moment of use:

| Logical name | What it is |
|---|---|
| `billing_provider` | the billing provider's server-side API key |
| `billing_webhook` | the signing secret inbound webhooks are verified against |

`internal/billing/secrets.go` states the reasoning plainly: a second mechanism for billing would split
the truth in two, and the failure mode is *"a deployment whose LLM credentials come from a manager while
its BILLING credentials quietly come from an environment variable, with a health endpoint that is
confidently wrong about both."* Billing inherits the manager, the caching, the fail-closed behaviour and
the health signal for free. Both fail **closed**: no key means no provider call and no webhook
verification, never a fallback to an unauthenticated path.

🔴 **Two things to know before you plan billing on AWS:**

1. **It is not deployable yet, for a reason that is not the secrets.** `billing.Ledger` and
   `account.Store` each have exactly one implementation and both are in-memory, so P7/P21 are
   registered-but-not-mounted and `POST /billing/webhook` is not registered at all (§10). No Stripe
   key is needed today.
2. **When it does ship, `HEROS_SECRETS_AWS_PREFIX` alone will not find these.** The prefix expands over
   `SupportedProviders()` — `openai`, `anthropic`, `bedrock` — which does **not** include the two billing
   names, and `awsSecretIDs()` takes `_IDS` *or* `_PREFIX`, never both. So enabling billing means
   switching that pod to `HEROS_SECRETS_AWS_IDS` and enumerating every name explicitly:

   ```
   HEROS_SECRETS_AWS_IDS=openai=heros/providers/openai,anthropic=heros/providers/anthropic,\
   billing_provider=heros/billing/api-key,billing_webhook=heros/billing/webhook-signing
   ```

**Sentry is `HEROS_ERROR_REPORTING_DSN`.** The variable is vendor-neutral by name and Sentry by wire —
the reporter posts an `application/x-sentry-envelope`. So a Sentry DSN is exactly what it takes. There
is no `SENTRY_DSN` and no `NEXT_PUBLIC_SENTRY_DSN`; both are on the **forbidden** list the P24 build
fence enforces, alongside the DSN itself, because none of them may appear in a deployment manifest even
empty (A.4). Configure it out-of-band on your own hosted deployment, and remember it is a **hard boot
failure when set and unusable**.

### A.6 CLI sign-in vs browser SSO — two principals, no overlap

They do not interact, and neither replaces the other. The confusion is worth resolving because both are
called "signing in".

| | **`heros login`** (CLI) | **Browser sign-in** (console) |
|---|---|---|
| Who is authenticating | a **machine** — a developer's shell, or CI | a **person**, in a browser |
| Credential | a platform **bearer token** | an IdP assertion, or a configured assertion |
| Where it comes from | `--token`, `$HEROS_PLATFORM_TOKEN`, or piped on stdin — **never a prompt**, so it cannot land in shell history or `ps` | your IdP (§A.0), or the `CONSOLE_TENANT_ASSERTIONS` map |
| What it hits | `GET /api/v1/whoami` on the platform | `/auth/login` → `/auth/callback` on the console |
| What it produces | a credential file at `0600` | an `HttpOnly` session cookie bound server-side to a tenant |
| Configured by | nothing in this appendix — the endpoint is a compiled-in constant | `CONSOLE_TENANT_IDENTITY` + the `CONSOLE_IDP_*` set |

**There is no bridge between them**, and that is current design rather than an omission: the P11 PRD
records that a **device-code flow — the thing that would let `heros login` open a browser and reuse
SSO — was explicitly deferred**. So SSO does not issue CLI tokens, and a CLI token cannot open a
console session. How a user obtains their platform token is, today, out of band.

See §7.4 for why the CLI path additionally does not resolve on this deployment topology.
