#!/bin/bash
set -e

psql -f postgresql/prepare.sql

[ ! -f postgresql/tern.conf ] && cp postgresql/tern.example.conf postgresql/tern.conf

mise trust
mise install
eval "$(mise env -s bash)"
bundle install
npm install
npx playwright install --with-deps chromium

tern migrate
PGDATABASE=logger4life_test tern migrate

# Run any additional setup scripts included on the shared volume. This is to allow for per developer or
# per-environment customizations. These scripts are not checked into source control.
if [ -x "/persist/shared/devcontainer/install" ]; then
  /persist/shared/devcontainer/install
fi

# Create a symlink to the shared scratch directory (on the persistent shared volume) for temporary files.
if [ ! -e .scratch ] && [ ! -L .scratch ]; then
  ln -s /persist/shared/scratch .scratch
fi
