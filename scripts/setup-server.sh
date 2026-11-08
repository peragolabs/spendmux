#!/bin/sh
# setup-server.sh [USER]
#
# One-time (and safe to re-run) server preparation for Ubuntu or Debian:
#
#   1. updates the system packages;
#   2. installs or updates Docker Engine and the Compose plugin from Docker's
#      official apt repository;
#   3. creates /opt/apps, owned by USER (default: the user who ran sudo), for
#      the git checkout;
#   4. creates a GitHub deploy key for USER (~/.ssh/spendmux_deploy), pins
#      GitHub's host key, and prints the public key to add on GitHub.
#
# Run it as root:  sudo sh setup-server.sh
# It does not clone the repository, create .env, or start anything.
set -eu

[ "$(id -u)" = 0 ] || { echo "run as root: sudo sh $0" >&2; exit 1; }

owner="${1:-${SUDO_USER:-root}}"
id "$owner" >/dev/null 2>&1 || { echo "no such user: $owner" >&2; exit 1; }

. /etc/os-release
case "$ID" in
	ubuntu | debian) ;;
	*) echo "unsupported OS: $ID (this script handles Ubuntu and Debian)" >&2; exit 1 ;;
esac

export DEBIAN_FRONTEND=noninteractive

echo "==> Updating system packages"
apt-get update -q
apt-get upgrade -y -q
apt-get install -y -q ca-certificates curl git openssh-client

echo "==> Adding Docker's apt repository"
install -m 0755 -d /etc/apt/keyrings
curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID ${VERSION_CODENAME} stable" \
	> /etc/apt/sources.list.d/docker.list

echo "==> Installing or updating Docker"
apt-get update -q
apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker

echo "==> Creating /opt/apps for $owner"
install -d -m 0750 -o "$owner" -g "$owner" /opt/apps

echo "==> Creating a GitHub deploy key for $owner"
# A read-only deploy key for one repository, made here so the private half
# never leaves this server. GitHub's host key is pinned (from
# https://api.github.com/meta, "ssh_keys"), so the first clone needs no
# trust-on-first-use prompt and cannot be pointed at an impostor.
home=$(getent passwd "$owner" | cut -d: -f6)
key="$home/.ssh/spendmux_deploy"
as_owner() { runuser -u "$owner" -- "$@"; }
as_owner install -d -m 0700 "$home/.ssh"
if [ -f "$key" ]; then
	echo "    $key already exists, keeping it"
else
	as_owner ssh-keygen -q -t ed25519 -N "" -C "spendmux-deploy@$(hostname)" -f "$key"
fi
grep -qs "^github.com ssh-ed25519 " "$home/.ssh/known_hosts" ||
	echo "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl" | as_owner tee -a "$home/.ssh/known_hosts" >/dev/null
grep -qs "spendmux_deploy" "$home/.ssh/config" ||
	printf 'Host github.com\n  IdentityFile %s\n  IdentitiesOnly yes\n' "$key" | as_owner tee -a "$home/.ssh/config" >/dev/null
chmod 600 "$home/.ssh/config" "$home/.ssh/known_hosts"

echo
docker --version
docker compose version
cat <<EOF

Done. Add this deploy key on GitHub: repo -> Settings -> Deploy keys ->
Add deploy key. Leave "Allow write access" unchecked.

$(cat "$key.pub")

Then, as $owner:
  ssh -T git@github.com     (should greet the repository by name)
  cd /opt/apps && git clone git@github.com:peragolabs/spendmux.git
  then copy your production .env into the checkout and let only you and the
  app container (uid/gid 10001) read it:
    sudo chown $owner:10001 .env && chmod 640 .env

To run docker without sudo, add $owner to the docker group:
  sudo usermod -aG docker $owner   (then log out and back in)
Members of that group are effectively root on this server.
EOF
