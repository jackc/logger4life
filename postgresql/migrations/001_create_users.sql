CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT uuidv4(),
    username varchar(30) NOT NULL CHECK (username ~ '\A[a-zA-Z0-9_]+\Z'),
    email varchar(254),
    password_hash varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_username_unq ON users (lower(username));
CREATE UNIQUE INDEX users_email_unq ON users (lower(email)) WHERE email IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON users TO {{.app_user}};

---- create above / drop below ----

DROP TABLE users;
