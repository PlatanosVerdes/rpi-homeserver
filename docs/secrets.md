# Secrets: an encrypted .env per machine

`.env` holds every secret a node needs and is never committed to this public repo. On its own it
lives only on the Pi: it has no history, no copy elsewhere, and editing it means an ssh session.
This keeps it **encrypted in a private repo** instead and lets the deploy write `.env` from it.

Nothing here is required. A node without the checkout described below, or without `sops`, uses
its `.env` as before.

## Terms

| Term | What it means here |
| :--- | :--- |
| **age** | A small file-encryption tool. A key pair is one line each: the public key (`age1…`) encrypts, the private key decrypts and never leaves the machine that made it |
| **SOPS** | Encrypts the *values* of a structured file and leaves the keys readable, so a diff still shows which variable changed. It reads `.env` files as `dotenv` |
| **Recipient** | A public key a file is encrypted to. Any one of the matching private keys can decrypt it |

## Layout

A private repository, e.g. `homelab-secrets`, cloned at `~/homelab-secrets` on each node:

```
.sops.yaml                  which recipients each path is encrypted to
raspi/rpi-homeserver.env    this repo's .env on the node called raspi
raspi/rpi-services.env      the companion repo's .env on the same node
streamhalla/rpi-homeserver.env
```

The directory is the node's **Tailscale name** (`tailscale status`, first label of its DNS name),
falling back to `hostname -s`. Each directory is encrypted to that node's key and to the
administrator's key, so a node can read only its own files and one laptop can edit all of them:

```yaml
creation_rules:
  - path_regex: ^raspi/.*\.env$
    age: age1<raspi>,age1<laptop>
  - path_regex: ^streamhalla/.*\.env$
    age: age1<streamhalla>,age1<laptop>
```

Why a private repo and not this one: the encrypted files would be fine to publish, but published is
forever. A key that leaks one day would open every secret in the history, and a private repo means
an attacker needs both the key and the repository.

## What the deploy does

At the start of `scripts/deploy/apply.sh`, before anything reads `.env`, when `~/homelab-secrets`
is a git checkout and `sops` is installed:

1. `git pull --ff-only` in the secrets checkout (a failure is logged and the copy on disk is used).
2. For this repo and the companion one, decrypt `<node>/<repo>.env` and replace `.env` (mode 600)
   only if the result differs, logging `[secrets] … updated`.
3. A file that does not decrypt is logged and the current `.env` is kept.

The usual `up -d` then applies whatever changed: a secret edited in the private repo reaches the
containers that use it on the next pass, with no ssh.

SOPS does not keep blank lines, so the first decrypted `.env` loses them; comments and values are
byte-for-byte the same.

## Setting up a node

```bash
# Tools: age from Debian, sops from its release page (not packaged), checksum verified
sudo apt install age
V=v3.13.3
curl -fsSLO https://github.com/getsops/sops/releases/download/$V/sops-$V.linux.arm64
curl -fsSLO https://github.com/getsops/sops/releases/download/$V/sops-$V.checksums.txt
grep " sops-$V.linux.arm64$" sops-$V.checksums.txt | sha256sum -c -
sudo install -m 755 sops-$V.linux.arm64 /usr/local/bin/sops

# The node's key, where sops looks by default on Linux
mkdir -p ~/.config/sops/age && age-keygen -o ~/.config/sops/age/keys.txt
chmod 600 ~/.config/sops/age/keys.txt
age-keygen -y ~/.config/sops/age/keys.txt     # the public key, for .sops.yaml

# Read-only access to the private repo: a deploy key, one per node
ssh-keygen -t ed25519 -N "" -f ~/.ssh/homelab_secrets -C "<node> homelab-secrets"
# add ~/.ssh/homelab_secrets.pub as a read-only deploy key on the repo, then:
cat >> ~/.ssh/config <<'CFG'
Host github-homelab-secrets
    HostName github.com
    User git
    IdentityFile ~/.ssh/homelab_secrets
    IdentitiesOnly yes
CFG
git clone git@github-homelab-secrets:<owner>/homelab-secrets.git ~/homelab-secrets
```

Encrypt the node's current `.env` **on the node**, so the plain file never travels:

```bash
sops encrypt --age age1<node>,age1<laptop> --input-type dotenv --output-type dotenv ~/rpi-homeserver/.env
```

and commit the output as `<node>/rpi-homeserver.env`.

## Editing a secret

On the laptop, in the secrets checkout:

```bash
sops raspi/rpi-homeserver.env      # opens the decrypted file in $EDITOR, re-encrypts on save
git commit -am "Rotate the Plex token" && git push
```

The node picks it up on its next deploy pass (30 minutes at most, or push anything to this repo).
Editing `.env` on the Pi directly still works, but the next pass replaces it with the decrypted
copy: change the secret in the private repo instead.

## Keys: where they are and what losing one means

| Key | Where | If it is lost | If it leaks |
| :--- | :--- | :--- | :--- |
| A node's private key | `~/.config/sops/age/keys.txt` on that node | Make a new one, add its public key to `.sops.yaml`, `sops updatekeys` the node's files from the laptop | Same, and rotate the secrets in that node's files |
| The laptop's private key | The laptop, plus a copy in a password manager | Restore it from the password manager | Rotate every secret, since it reads all files |

The private keys are not in GitHub or CI: nothing there needs to decrypt.
