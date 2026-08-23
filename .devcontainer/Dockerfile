FROM mcr.microsoft.com/devcontainers/base:ubuntu24.04

# The container no longer runs a PostgreSQL service of its own: each worktree
# runs its own cluster out of .dev/, exactly as on macOS. What is needed here
# is the PostgreSQL 18 server and client binaries, from the PostgreSQL repo.
# Debian's packaging would otherwise create and start a machine-wide cluster,
# which is precisely what this environment replaces.
RUN mkdir -p /etc/postgresql-common \
    && echo "create_main_cluster = false" > /etc/postgresql-common/createcluster.conf \
    && apt-get update \
    && apt-get install -y postgresql-common \
    && yes | /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh \
    && apt-get install -y postgresql-18 \
    && rm -rf /var/lib/apt/lists/*

# Install Ruby from source prerequisites.
RUN apt-get update \
    && apt-get install -y build-essential libssl-dev libreadline-dev zlib1g-dev libffi-dev ncurses-dev libyaml-dev libxml2 libxml2-dev libxslt1-dev \
    && rm -rf /var/lib/apt/lists/*
