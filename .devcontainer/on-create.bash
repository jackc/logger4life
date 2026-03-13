#!/bin/bash
set -e

SCRIPTDIR=$(dirname -- "$(readlink -f -- "$0")")
"$SCRIPTDIR/fd/install"
"$SCRIPTDIR/rg/install"
"$SCRIPTDIR/tern/install"
"$SCRIPTDIR/verna/install"
"$SCRIPTDIR/watchexec/install"
