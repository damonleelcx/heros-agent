#!/usr/bin/env bash
# Provision the EC2 host this mail server runs on. Idempotent: re-running converges and never
# replaces a live instance.
#
#   bash provision-ec2.sh <domain>
#
# Environment it reads (all optional except where noted):
#   AWS_REGION      default us-east-1
#   VPC_ID          default the region's default VPC
#   SUBNET_ID       default the first PUBLIC subnet in that VPC
#   KEY_NAME        REQUIRED — an existing EC2 key pair, or you cannot get in
#   ADMIN_CIDR      who may reach SSH and IMAPS. Default 0.0.0.0/0 with a warning; set it.
#   APP_CIDR        who may reach submission on 587/465. Default: the VPC's CIDR.
#   INSTANCE_TYPE   default t3.small
#
# ─────────────────────────────────────────────────────────────────────────────────────────────────
# ⚠️ WHAT THIS DELIBERATELY DOES NOT DO
#
# It does not request the port-25 removal, and it cannot: that is a Support case a human files, and
# it is reviewed by a person (README §2). It does not create DNS records — the domain may not be in
# Route 53, and a script that guesses wrong about DNS breaks mail for the whole domain rather than
# just this host. And it does not run the bootstrap: the bootstrap needs the A record, which needs
# the Elastic IP this script prints at the end.
#
# So the order is: run this → publish the A record → SSH in → bootstrap → dns-records → verify.
set -euo pipefail

DOMAIN="${1:?usage: provision-ec2.sh <domain>}"
REGION="${AWS_REGION:-us-east-1}"
INSTANCE_TYPE="${INSTANCE_TYPE:-t3.small}"
NAME="heros-mail-${DOMAIN//./-}"
: "${KEY_NAME:?set KEY_NAME to an existing EC2 key pair — without it this instance is unreachable}"

log()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!  %s\033[0m\n' "$*"; }
aws_() { aws --region "$REGION" "$@"; }

aws_ sts get-caller-identity >/dev/null || { echo "AWS credentials are not valid — 'aws login' first" >&2; exit 2; }

# ── VPC and subnet ──────────────────────────────────────────────────────────────────────────────
VPC_ID="${VPC_ID:-$(aws_ ec2 describe-vpcs --filters Name=isDefault,Values=true \
  --query 'Vpcs[0].VpcId' --output text)}"
[ "$VPC_ID" != "None" ] || { echo "no default VPC — set VPC_ID" >&2; exit 2; }
VPC_CIDR="$(aws_ ec2 describe-vpcs --vpc-ids "$VPC_ID" --query 'Vpcs[0].CidrBlock' --output text)"

if [ -z "${SUBNET_ID:-}" ]; then
  # 🔴 A PUBLIC subnet specifically. An instance in a private subnet reaches the internet through a
  # NAT gateway, and a NAT gateway's address is not the Elastic IP — so SPF, PTR and every
  # reputation signal would belong to an address this host does not control. Mail from a private
  # subnet is mail from an IP you cannot get rDNS on.
  SUBNET_ID="$(aws_ ec2 describe-subnets --filters "Name=vpc-id,Values=${VPC_ID}" \
    "Name=map-public-ip-on-launch,Values=true" --query 'Subnets[0].SubnetId' --output text)"
fi
[ -n "$SUBNET_ID" ] && [ "$SUBNET_ID" != "None" ] || {
  echo "no public subnet found in ${VPC_ID} — set SUBNET_ID to one that routes to an internet gateway" >&2
  exit 2; }

ADMIN_CIDR="${ADMIN_CIDR:-0.0.0.0/0}"
APP_CIDR="${APP_CIDR:-$VPC_CIDR}"
[ "$ADMIN_CIDR" = "0.0.0.0/0" ] && warn "ADMIN_CIDR is 0.0.0.0/0 — SSH and IMAPS are open to the internet. Set it to your office/VPN range."

log "region=${REGION} vpc=${VPC_ID} (${VPC_CIDR}) subnet=${SUBNET_ID}"

# ── Security group ──────────────────────────────────────────────────────────────────────────────
SG_ID="$(aws_ ec2 describe-security-groups --filters "Name=vpc-id,Values=${VPC_ID}" \
  "Name=group-name,Values=${NAME}" --query 'SecurityGroups[0].GroupId' --output text 2>/dev/null || echo None)"
if [ "$SG_ID" = "None" ] || [ -z "$SG_ID" ]; then
  log "Creating security group ${NAME}"
  SG_ID="$(aws_ ec2 create-security-group --group-name "$NAME" --vpc-id "$VPC_ID" \
    --description "heros-agent mail relay" --query GroupId --output text)"
else
  log "Security group ${SG_ID} exists"
fi

# authorize PORT CIDR DESC — ignores the duplicate error so a re-run converges.
authorize() {
  aws_ ec2 authorize-security-group-ingress --group-id "$SG_ID" \
    --ip-permissions "IpProtocol=tcp,FromPort=$1,ToPort=$1,IpRanges=[{CidrIp=$2,Description=\"$3\"}]" \
    >/dev/null 2>&1 || true
}
# 25 must be world-open INBOUND, and that is not the same thing as the outbound block AWS applies.
# Inbound 25 is how bounces and DMARC reports get back; refusing it means the only evidence that our
# own mail is failing never arrives.
authorize 25  0.0.0.0/0     "inbound mail: bounces, DMARC reports, replies"
authorize 80  0.0.0.0/0     "certbot HTTP-01 challenge"
authorize 587 "$APP_CIDR"   "submission (agentd)"
authorize 465 "$APP_CIDR"   "submission, implicit TLS"
authorize 993 "$ADMIN_CIDR" "IMAPS for the operator's mailboxes"
authorize 22  "$ADMIN_CIDR" "ssh"

# ── AMI ─────────────────────────────────────────────────────────────────────────────────────────
# Canonical's own SSM parameter rather than a pinned AMI ID: AMI IDs are per-region and are replaced
# on every security refresh, so a literal here is wrong in every region but one and stale everywhere.
AMI="$(aws_ ssm get-parameters \
  --names /aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id \
  --query 'Parameters[0].Value' --output text)"
log "AMI ${AMI} (Ubuntu 24.04)"

# ── Instance ────────────────────────────────────────────────────────────────────────────────────
IID="$(aws_ ec2 describe-instances \
  --filters "Name=tag:Name,Values=${NAME}" "Name=instance-state-name,Values=pending,running,stopped" \
  --query 'Reservations[0].Instances[0].InstanceId' --output text 2>/dev/null || echo None)"

if [ "$IID" = "None" ] || [ -z "$IID" ]; then
  log "Launching ${INSTANCE_TYPE}"
  IID="$(aws_ ec2 run-instances \
    --image-id "$AMI" --instance-type "$INSTANCE_TYPE" \
    --key-name "$KEY_NAME" --subnet-id "$SUBNET_ID" --security-group-ids "$SG_ID" \
    --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=20,VolumeType=gp3,Encrypted=true}' \
    --metadata-options 'HttpTokens=required,HttpEndpoint=enabled' \
    --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=${NAME}},{Key=app,Value=heros-agent},{Key=role,Value=mail}]" \
    --query 'Instances[0].InstanceId' --output text)"
  aws_ ec2 wait instance-running --instance-ids "$IID"
else
  log "Instance ${IID} exists — not replacing it"
fi

# ── Elastic IP ──────────────────────────────────────────────────────────────────────────────────
#
# 🔴 An Elastic IP, never the auto-assigned public IP. rDNS is granted for a SPECIFIC address, SPF
# names a specific address, and reputation accrues to a specific address. An auto-assigned IP changes
# on every stop/start, and when it does, all three break at once, silently, and the mail that was
# delivering yesterday goes to spam with nothing in any log to say why.
ALLOC="$(aws_ ec2 describe-addresses --filters "Name=tag:Name,Values=${NAME}" \
  --query 'Addresses[0].AllocationId' --output text 2>/dev/null || echo None)"
if [ "$ALLOC" = "None" ] || [ -z "$ALLOC" ]; then
  log "Allocating Elastic IP"
  ALLOC="$(aws_ ec2 allocate-address --domain vpc \
    --tag-specifications "ResourceType=elastic-ip,Tags=[{Key=Name,Value=${NAME}}]" \
    --query AllocationId --output text)"
fi
CURRENT="$(aws_ ec2 describe-addresses --allocation-ids "$ALLOC" \
  --query 'Addresses[0].InstanceId' --output text)"
if [ "$CURRENT" != "$IID" ]; then
  aws_ ec2 associate-address --instance-id "$IID" --allocation-id "$ALLOC" >/dev/null
fi
EIP="$(aws_ ec2 describe-addresses --allocation-ids "$ALLOC" --query 'Addresses[0].PublicIp' --output text)"

cat <<EOF

  ─────────────────────────────────────────────────────────────────────────────────────────────
  instance   ${IID}
  elastic ip ${EIP}
  sg         ${SG_ID}
  ─────────────────────────────────────────────────────────────────────────────────────────────

  Next, in this order — each step is a precondition of the one after it:

  1. Publish  A  mail.${DOMAIN} → ${EIP}
     certbot cannot issue until this resolves, and the bootstrap cannot finish without certbot.

  2. scp deploy/mail/*.sh ubuntu@${EIP}:~/ && ssh ubuntu@${EIP}

     sudo SMARTHOST_HOST=… SMARTHOST_USER=… SMARTHOST_PASS=… SMARTHOST_SPF=… \\
       bash bootstrap-mailserver.sh ${DOMAIN}

     smarthost is the DEFAULT. Credentials go in the ENVIRONMENT, not argv — a password on a
     command line is in the shell history and in every process listing while this runs.

  3. sudo bash dns-records.sh ${DOMAIN}   → publish the records it prints, INCLUDING the relay's
     own (its step 6). The relay will not send as ${DOMAIN} until those are up.

  4. sudo bash verify-mail.sh ${DOMAIN} you@example.com

  5. Pin ${EIP} into deploy/k8s/overlays/prod/kustomization.yaml — the agentd egress rule for
     port 587. \`make deploy-lint\` fails until you do.

  ⚠️ ONLY IF YOU CHOOSE 'direct' MODE: request rDNS + port-25 removal for ${EIP} first (README §2).
  It is reviewed by a person, takes a day or more, and nothing delivers until it lands. Smarthost
  mode needs neither.

EOF
