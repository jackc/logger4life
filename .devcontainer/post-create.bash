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

# Put mise shims on PATH for non-interactive shells. Those shells read
# ~/.zprofile but never ~/.zshrc, where the oh-my-zsh mise plugin does
# hook-based activation -- and its hooks only fire from precmd/chpwd, so a
# non-interactive shell ends up with no go/ruby/rake. Shims are a plain PATH
# prepend, so they work everywhere. Interactive shells still get the full
# `mise activate` from ~/.zshrc, which prepends ahead of these shims.
if ! grep -q 'mise activate zsh --shims' ~/.zprofile 2>/dev/null; then
  cat >> ~/.zprofile <<'EOF'

if command -v mise >/dev/null 2>&1; then
  eval "$(mise activate zsh --shims)"
fi
EOF
fi

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
