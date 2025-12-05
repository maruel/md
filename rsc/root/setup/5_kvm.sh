#!/bin/bash
# Make sure kvm is accessible to user inside the container.
set -euo pipefail
echo "- $0"

usermod -a -G kvm user
