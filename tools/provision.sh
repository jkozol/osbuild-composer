#!/bin/bash
set -euxo pipefail

source /etc/os-release

sudo mkdir -p /etc/osbuild-composer
sudo cp -a /usr/share/tests/osbuild-composer/composer/*.toml \
    /etc/osbuild-composer/

# Copy rpmrepo snapshots for use in weldr tests
sudo mkdir -p /etc/osbuild-composer/repositories
# Copy all fedora repo overrides
sudo cp -a /usr/share/tests/osbuild-composer/repositories/fedora-*.json \
    /etc/osbuild-composer/repositories/
# RHEL repos need to be overriden in rhel-8.json and rhel-8-beta.json
case "${ID}-${VERSION_ID}" in
    "rhel-8.3")
        # Override old rhel-8.json and rhel-8-beta.json because test needs latest systemd and redhat-release
        sudo cp /usr/share/tests/osbuild-composer/repositories/rhel-83.json /etc/osbuild-composer/repositories/rhel-8.json
        sudo ln -s /etc/osbuild-composer/repositories/rhel-8.json /etc/osbuild-composer/repositories/rhel-8-beta.json;;
    *) ;;
esac

sudo cp -a /usr/share/tests/osbuild-composer/ca/* \
    /etc/osbuild-composer/
sudo chown _osbuild-composer /etc/osbuild-composer/composer-*.pem

sudo systemctl start osbuild-remote-worker.socket
sudo systemctl start osbuild-composer.socket
sudo systemctl start osbuild-composer-api.socket

# Basic verification
sudo composer-cli status show
sudo composer-cli sources list
for SOURCE in $(sudo composer-cli sources list); do
    sudo composer-cli sources info "$SOURCE"
done
