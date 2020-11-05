#!/bin/bash

set -euxo pipefail
# we need this for ansible and koji
dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-8.noarch.rpm
dnf install -y osbuild-composer-tests
