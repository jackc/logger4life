//! Password and cookie sessions. The database representation is shared with Go.
use crate::{
    App, AppError, Request, Response, Result,
    store::{Conn, bytes, timestamp},
};
use chrono::{Duration, Utc};
use http::{HeaderMap, HeaderValue, header};
use rand::RngCore;
use serde::Deserialize;
use serde_json::{Value, json};
use uuid::Uuid;

pub fn random_bytes() -> [u8; 32] {
    let mut value = [0; 32];
    rand::rng().fill_bytes(&mut value);
    value
}

pub fn public_user(mut user: Value) -> Value {
    if let Some(object) = user.as_object_mut() {
        object.remove("password_hash");
        if object.get("email").is_some_and(Value::is_null) {
            object.remove("email");
        }
    }
    user
}

pub fn cookie_token(headers: &HeaderMap) -> Option<String> {
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|h| h.to_str().ok())
        .flat_map(|h| h.split(';'))
        .find_map(|item| {
            let (name, value) = item.trim().split_once('=')?;
            (name == "session_token").then(|| value.to_owned())
        })
}

pub fn load_session(app: &App, headers: &HeaderMap) -> Result<Option<Value>> {
    let Some(token) = cookie_token(headers)
        .and_then(|t| hex::decode(t).ok())
        .filter(|t| !t.is_empty())
    else {
        return Ok(None);
    };
    app.db.read(|conn| Ok(conn.query("SELECT u.id, u.username, u.email FROM sessions s JOIN users u ON s.user_id = u.id WHERE s.token = $1 AND s.expires_at > now()", &[bytes(&token)])?.into_iter().next().map(public_user)))
}

pub fn create_session(conn: &mut Conn, user_id: &str) -> Result<String> {
    let token = random_bytes();
    conn.execute(
        "INSERT INTO sessions (id, user_id, token, expires_at) VALUES ($1, $2, $3, $4)",
        &[
            json!(Uuid::now_v7().to_string()),
            json!(user_id),
            bytes(&token),
            timestamp(Utc::now() + Duration::days(30)),
        ],
    )?;
    Ok(hex::encode(token))
}

pub fn session_response(app: &App, status: u16, user: Value, token: &str) -> Response {
    let mut response = Response::json(status, public_user(user));
    let expires = (Utc::now() + Duration::days(30)).format("%a, %d %b %Y %H:%M:%S GMT");
    let cookie = format!(
        "session_token={token}; Path=/; HttpOnly; SameSite=Lax; Max-Age=2592000; Expires={expires}{}",
        if app.config.secure_cookies {
            "; Secure"
        } else {
            ""
        }
    );
    response.headers.insert(
        header::SET_COOKIE,
        HeaderValue::from_str(&cookie).expect("session cookie"),
    );
    response
}

pub fn clear_session_cookie(app: &App, response: &mut Response) {
    let cookie = format!(
        "session_token=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0{}",
        if app.config.secure_cookies {
            "; Secure"
        } else {
            ""
        }
    );
    response.headers.insert(
        header::SET_COOKIE,
        HeaderValue::from_str(&cookie).expect("session cookie"),
    );
}

fn validate_password(password: &str, field: &str) -> Result<()> {
    if password.len() < 8 {
        return Err(AppError::bad_request(format!(
            "{field} must be at least 8 characters"
        )));
    }
    if password.len() > 72 {
        return Err(AppError::bad_request(format!(
            "{field} must be at most 72 characters"
        )));
    }
    Ok(())
}

fn email(value: Option<String>) -> Value {
    value
        .map(|v| v.trim().to_owned())
        .filter(|v| !v.is_empty())
        .map_or(Value::Null, Value::String)
}

pub fn handle(app: &App, req: &Request) -> Option<Result<Response>> {
    let result = match (req.method.as_str(), req.path.as_str()) {
        ("POST", "/api/register") => register(app, req),
        ("POST", "/api/login") => login(app, req),
        ("POST", "/api/logout") => logout(app, req),
        ("GET", "/api/me") => req.user_id().and_then(|id| {
            app.db.read(|conn| {
                let user = conn
                    .query(
                        "SELECT id, username, email FROM users WHERE id = $1",
                        &[json!(id)],
                    )?
                    .into_iter()
                    .next()
                    .ok_or_else(|| AppError::unauthorized("not authenticated"))?;
                Ok(Response::json(200, public_user(user)))
            })
        }),
        ("PUT", "/api/me/email") => change_email(app, req),
        ("PUT", "/api/me/password") => change_password(app, req),
        _ => return None,
    };
    Some(result)
}

#[derive(Deserialize)]
struct Credentials {
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    email: Option<String>,
}

fn register(app: &App, req: &Request) -> Result<Response> {
    if !app.config.allow_registration {
        return Err(AppError::forbidden("registration is currently disabled"));
    }
    let params: Credentials = req.json()?;
    let username = params.username.trim();
    if username.is_empty()
        || username.len() > 30
        || !username
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'_')
    {
        return Err(AppError::bad_request(
            "username must be 1-30 letters, digits, or underscores",
        ));
    }
    validate_password(&params.password, "password")?;
    let hash = bcrypt::hash(&params.password, 10).map_err(AppError::internal)?;
    let email = email(params.email);
    let (user, token) = app.db.transaction(|conn| {
        if !conn.query("SELECT id FROM users WHERE lower(username) = lower($1)", &[json!(username)])?.is_empty() {
            return Err(AppError::conflict("username already taken"));
        }
        if !email.is_null() && !conn.query("SELECT id FROM users WHERE lower(email) = lower($1)", std::slice::from_ref(&email))?.is_empty() {
            return Err(AppError::conflict("email already in use"));
        }
        let id = Uuid::new_v4().to_string();
        let user = conn.query("INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, $4) RETURNING id, username, email", &[json!(id), json!(username), email, json!(hash)])?.remove(0);
        let token = create_session(conn, &id)?;
        Ok((user, token))
    })?;
    Ok(session_response(app, 201, user, &token))
}

fn login(app: &App, req: &Request) -> Result<Response> {
    let params: Credentials = req.json()?;
    let user = app.db.read(|conn| conn.query("SELECT id, username, email, password_hash FROM users WHERE lower(username) = lower($1)", &[json!(params.username)]))?.into_iter().next();
    let user = user
        .filter(|u| {
            bcrypt::verify(&params.password, u["password_hash"].as_str().unwrap_or(""))
                .unwrap_or(false)
        })
        .ok_or_else(|| AppError::unauthorized("invalid username or password"))?;
    let token = app
        .db
        .transaction(|conn| create_session(conn, user["id"].as_str().unwrap()))?;
    Ok(session_response(app, 200, user, &token))
}

fn logout(app: &App, req: &Request) -> Result<Response> {
    req.user_id()?;
    if let Some(token) = cookie_token(&req.headers).and_then(|t| hex::decode(t).ok()) {
        app.db.transaction(|conn| {
            conn.execute("DELETE FROM sessions WHERE token = $1", &[bytes(&token)])
        })?;
    }
    let mut response = Response::json(200, json!({"message":"logged out"}));
    clear_session_cookie(app, &mut response);
    Ok(response)
}

fn change_email(app: &App, req: &Request) -> Result<Response> {
    let id = req.user_id()?;
    #[derive(Deserialize)]
    struct Params {
        email: Option<String>,
    }
    let params: Params = req.json()?;
    let email = email(params.email);
    let user = app.db.transaction(|conn| {
        if !email.is_null() && !conn.query("SELECT id FROM users WHERE lower(email) = lower($1) AND id <> $2", &[email.clone(), json!(id)])?.is_empty() {
            return Err(AppError::conflict("email already in use"));
        }
        conn.query("UPDATE users SET email = $1, updated_at = now() WHERE id = $2 RETURNING id, username, email", &[email, json!(id)])?.into_iter().next().ok_or_else(|| AppError::unauthorized("not authenticated"))
    })?;
    Ok(Response::json(200, public_user(user)))
}

fn change_password(app: &App, req: &Request) -> Result<Response> {
    let id = req.user_id()?;
    #[derive(Deserialize)]
    struct Params {
        #[serde(default)]
        current_password: String,
        #[serde(default)]
        new_password: String,
    }
    let params: Params = req.json()?;
    validate_password(&params.new_password, "new password")?;
    let user = app
        .db
        .read(|conn| {
            conn.query(
                "SELECT password_hash FROM users WHERE id = $1",
                &[json!(id)],
            )
        })?
        .into_iter()
        .next()
        .ok_or_else(|| AppError::unauthorized("not authenticated"))?;
    if !bcrypt::verify(
        &params.current_password,
        user["password_hash"].as_str().unwrap_or(""),
    )
    .unwrap_or(false)
    {
        return Err(AppError::forbidden("current password is incorrect"));
    }
    let hash = bcrypt::hash(&params.new_password, 10).map_err(AppError::internal)?;
    app.db.transaction(|conn| {
        conn.execute(
            "UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2",
            &[json!(hash), json!(id)],
        )
    })?;
    Ok(Response::json(200, json!({"message":"password updated"})))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn password_length_matches_bcrypt_byte_limit() {
        assert!(validate_password(&"a".repeat(72), "password").is_ok());
        assert!(validate_password(&"é".repeat(37), "password").is_err());
        assert!(validate_password("short", "password").is_err());
    }
    #[test]
    fn public_identity_never_leaks_password() {
        assert_eq!(
            public_user(json!({"id":"a","username":"alice","email":null,"password_hash":"secret"})),
            json!({"id":"a","username":"alice"})
        );
    }
}
