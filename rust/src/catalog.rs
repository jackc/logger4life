//! Log, entry, organization, sharing and saved-query application operations.
mod domain;

use crate::{
    App, AppError, Request, Response, Result,
    store::{self, Conn},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use uuid::Uuid;

const VISIBLE: &str =
    "(l.user_id=$1 OR EXISTS (SELECT 1 FROM log_shares ls WHERE ls.log_id=l.id AND ls.user_id=$1))";
const FOLDER_COLUMNS: &str = "id,name,parent_folder_id,position,created_at,updated_at";
const SAVED_COLUMNS: &str = "id,name,query_text,created_at,updated_at";

fn logs(c: &Conn) -> &'static str {
    if c.is_jed() { "all_logs" } else { "logs" }
}
fn entries(c: &Conn) -> &'static str {
    if c.is_jed() {
        "all_log_entries"
    } else {
        "log_entries"
    }
}
fn first(rows: Vec<Value>, message: &str) -> Result<Value> {
    rows.into_iter()
        .next()
        .ok_or_else(|| AppError::not_found(message))
}
fn integer(row: &Value, key: &str) -> i64 {
    row[key].as_i64().unwrap_or(0)
}
fn changed(n: u64, message: &str) -> Result<()> {
    if n == 0 {
        Err(AppError::not_found(message))
    } else {
        Ok(())
    }
}
fn raw_id() -> Value {
    json!(Uuid::now_v7().to_string())
}
fn null_fields(row: &mut Value, value: Value) {
    if row["fields"].is_null() {
        row["fields"] = value;
    }
}

#[derive(Default, Deserialize)]
struct LogInput {
    name: Option<String>,
    fields: Option<Vec<domain::Field>>,
}
#[derive(Default, Deserialize)]
struct EntryInput {
    note: Option<String>,
    fields: Option<Map<String, Value>>,
    occurred_at: Option<DateTime<Utc>>,
}
#[derive(Default, Deserialize)]
struct FolderInput {
    name: Option<String>,
    parent_folder_id: Option<String>,
    position: Option<i64>,
}
#[derive(Default, Deserialize)]
struct PlacementInput {
    folder_id: Option<String>,
    position: Option<i64>,
    pinned: Option<bool>,
    home_position: Option<i64>,
}
#[derive(Default, Deserialize)]
struct SavedInput {
    name: Option<String>,
    query_text: Option<String>,
}

// Each HTTP request has its own accepted members. Unknown members, including
// fields belonging to another operation, are ignored as by Go's JSON decoder.
fn body<T: serde::de::DeserializeOwned>(req: &Request, accepted: &[&str]) -> Result<T> {
    let value: Value = req.json()?;
    let mut object = Map::new();
    match value {
        Value::Object(values) => {
            for (key, value) in values {
                if let Some(name) = accepted.iter().find(|name| key.eq_ignore_ascii_case(name)) {
                    object.insert((*name).to_owned(), value);
                }
            }
        }
        Value::Null => {}
        _ => return Err(AppError::bad_request("invalid request body")),
    }
    serde_json::from_value(Value::Object(object))
        .map_err(|_| AppError::bad_request("invalid request body"))
}

/// Only paths owned by this catalog are claimed; authentication precedes decoding.
pub fn handle(app: &App, req: &Request) -> Option<Result<Response>> {
    let path: Vec<&str> = req.path.trim_start_matches('/').split('/').collect();
    let owned = matches!(
        path.as_slice(),
        ["api", "logs", ..]
            | ["api", "folders", ..]
            | ["api", "join", _]
            | ["api", "sql", "saved", ..]
    );
    if !owned {
        return None;
    }
    Some((|| {
        let user = req.user_id()?;
        match (req.method.as_str(), path.as_slice()) {
            ("GET", ["api", "logs"]) => Ok(Response::json(200, list_logs(app, user)?)),
            ("POST", ["api", "logs"]) => Ok(Response::json(201, create_log(app, user, body(req, &["name", "fields"])?)?)),
            ("GET", ["api", "logs", id]) => { domain::id("log_id", id)?; Ok(Response::json(200, app.db.read(|c| get_log(c, user, id))?)) },
            ("PUT", ["api", "logs", id]) => Ok(Response::json(200, update_log(app, user, id, body(req, &["name", "fields"])?)?)),
            ("DELETE", ["api", "logs", id]) => { domain::id("log_id", id)?; delete_log(app, user, id)?; Ok(Response::empty(204)) },
            ("GET", ["api", "logs", id, "entries"]) => { domain::id("log_id", id)?; Ok(Response::json(200, list_entries(app, user, id)?)) },
            ("POST", ["api", "logs", id, "entries"]) => Ok(Response::json(201, write_entry(app, user, id, None, body(req, &["note", "fields"])?)?)),
            ("PUT", ["api", "logs", id, "entries", entry]) => Ok(Response::json(200, write_entry(app, user, id, Some(entry), body(req, &["note", "fields", "occurred_at"])?)?)),
            ("DELETE", ["api", "logs", id, "entries", entry]) => { delete_entry(app, user, id, entry)?; Ok(Response::empty(204)) },
            ("PUT", ["api", "logs", id, operation @ ("placement" | "pin" | "home-position")]) => { placement(app, user, id, operation, body(req, match *operation {"pin"=> &["pinned"], "home-position" => &["home_position"], _ => &["folder_id", "position"]})?)?; Ok(Response::empty(204)) },
            ("GET", ["api", "folders"]) => Ok(Response::json(200, app.db.read(|c| c.query(&format!("SELECT {FOLDER_COLUMNS} FROM folders WHERE user_id=$1 ORDER BY parent_folder_id NULLS FIRST,position"), &[json!(user)]))?)),
            ("POST", ["api", "folders"]) => Ok(Response::json(201, create_folder(app, user, body(req, &["name", "parent_folder_id"])?)?)),
            ("PUT", ["api", "folders", id]) => Ok(Response::json(200, rename_folder(app, user, id, body(req, &["name"])?)?)),
            ("PUT", ["api", "folders", id, "move"]) => { move_folder(app, user, id, body(req, &["parent_folder_id", "position"])?)?; Ok(Response::empty(204)) },
            ("DELETE", ["api", "folders", id]) => { delete_folder(app, user, id)?; Ok(Response::empty(204)) },
            ("POST", ["api", "logs", id, "share-token"]) => Ok(Response::json(200, share_token(app, user, id, true)?)),
            ("DELETE", ["api", "logs", id, "share-token"]) => { share_token(app, user, id, false)?; Ok(Response::empty(204)) },
            ("GET", ["api", "logs", id, "shares"]) => { domain::id("log_id", id)?; Ok(Response::json(200, app.db.read(|c| { owned_log(c, user, id)?; c.query("SELECT ls.id,u.username,ls.created_at AS shared_at FROM log_shares ls JOIN users u ON u.id=ls.user_id WHERE ls.log_id=$1 ORDER BY ls.created_at", &[json!(id)]) })?)) },
            ("DELETE", ["api", "logs", id, "shares", share]) => { domain::id("log_id", id)?; domain::id("share_id", share)?; app.db.transaction(|c| { owned_log(c, user, id)?; changed(c.execute("DELETE FROM log_shares WHERE id=$1 AND log_id=$2", &[json!(share),json!(id)])?, "share not found") })?; Ok(Response::empty(204)) },
            ("GET", ["api", "join", token]) => Ok(Response::json(200, share_info(app, user, token)?)),
            ("POST", ["api", "join", token]) => { let (result, existing) = join(app, user, token)?; Ok(Response::json(if existing {200} else {201}, result)) },
            ("GET", ["api", "sql", "saved"]) => Ok(Response::json(200, list_saved_queries(app, user)?)),
            ("POST", ["api", "sql", "saved"]) => Ok(Response::json(201, write_saved(app, user, None, body(req, &["name", "query_text"])?)?)),
            ("PUT", ["api", "sql", "saved", id]) => Ok(Response::json(200, write_saved(app, user, Some(id), body(req, &["name", "query_text"])?)?)),
            ("DELETE", ["api", "sql", "saved", id]) => { domain::id("id", id)?; app.db.transaction(|c| changed(c.execute("DELETE FROM saved_sql_queries WHERE id=$1 AND user_id=$2", &[json!(id),json!(user)])?, "saved query not found"))?; Ok(Response::empty(204)) },
            _ => Err(AppError::not_found("not found")),
        }
    })())
}

pub fn list_logs(app: &App, user: &str) -> Result<Value> {
    app.db.read(|c| {
        let mut rows = c.query(&format!("SELECT l.id,l.name,l.fields,l.user_id=$1 AS is_owner,p.folder_id,p.position,p.pinned_to_home,p.home_position,l.created_at,l.updated_at FROM {} l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE {VISIBLE} ORDER BY p.folder_id NULLS FIRST,p.position", logs(c)), &[json!(user)])?;
        for row in &mut rows { null_fields(row, json!([])); }
        Ok(json!(rows))
    })
}

fn get_log(c: &mut Conn, user: &str, id: &str) -> Result<Value> {
    let mut row = first(c.query(&format!("SELECT l.id,l.name,l.fields,l.user_id=$1 AS is_owner,l.share_token,p.folder_id,p.position,p.pinned_to_home,p.home_position,l.created_at,l.updated_at FROM {} l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE l.id=$2 AND {VISIBLE}", logs(c)), &[json!(user),json!(id)])?, "log not found")?;
    if row["is_owner"] != true || row["share_token"].is_null() {
        row.as_object_mut().unwrap().remove("share_token");
    }
    null_fields(&mut row, json!([]));
    Ok(row)
}

fn create_placement(c: &mut Conn, user: &str, id: &Value) -> Result<()> {
    let pos = first(c.query("SELECT COALESCE(max(position) FILTER(WHERE folder_id IS NULL)+1,0) AS position,COALESCE(max(home_position) FILTER(WHERE pinned_to_home)+1,0) AS home_position FROM user_log_placements WHERE user_id=$1", &[json!(user)])?, "placement not found")?;
    c.execute("INSERT INTO user_log_placements(user_id,log_id,folder_id,position,pinned_to_home,home_position) VALUES($1,$2,NULL,$3,true,$4) ON CONFLICT (user_id,log_id) DO NOTHING", &[json!(user),id.clone(),pos["position"].clone(),pos["home_position"].clone()])?;
    Ok(())
}

fn log_input(mut input: LogInput) -> Result<(String, Value)> {
    let name = domain::name(input.name.as_deref().unwrap_or_default())?;
    let fields = input.fields.get_or_insert_default();
    domain::definitions(fields)?;
    Ok((name, json!(fields)))
}

fn create_log(app: &App, user: &str, input: LogInput) -> Result<Value> {
    let (name, fields) = log_input(input)?;
    app.db.transaction(|c| {
        let id = raw_id();
        c.execute(
            &format!(
                "INSERT INTO {}(id,user_id,name,fields) VALUES($1,$2,$3,$4)",
                logs(c)
            ),
            &[id.clone(), json!(user), json!(name), fields],
        )?;
        create_placement(c, user, &id)?;
        get_log(c, user, id.as_str().unwrap())
    })
}

fn update_log(app: &App, user: &str, id: &str, input: LogInput) -> Result<Value> {
    domain::id("log_id", id)?;
    let (name, fields) = log_input(input)?;
    app.db.transaction(|c| {
        changed(
            c.execute(
                &format!(
                    "UPDATE {} SET name=$1,fields=$2,updated_at=now() WHERE id=$3 AND user_id=$4",
                    logs(c)
                ),
                &[json!(name), fields, json!(id), json!(user)],
            )?,
            "log not found",
        )?;
        get_log(c, user, id)
    })
}

fn owned_log(c: &mut Conn, user: &str, id: &str) -> Result<()> {
    first(
        c.query(
            &format!("SELECT id FROM {} WHERE id=$1 AND user_id=$2", logs(c)),
            &[json!(id), json!(user)],
        )?,
        "log not found",
    )
    .map(|_| ())
}

fn delete_log(app: &App, user: &str, id: &str) -> Result<()> {
    app.db.transaction(|c| {
        owned_log(c, user, id)?;
        for table in [entries(c), "log_shares", "user_log_placements"] {
            c.execute(
                &format!("DELETE FROM {table} WHERE log_id=$1"),
                &[json!(id)],
            )?;
        }
        c.execute(
            &format!("DELETE FROM {} WHERE id=$1", logs(c)),
            &[json!(id)],
        )?;
        Ok(())
    })
}

fn field_definitions(c: &mut Conn, user: &str, id: &str) -> Result<Vec<domain::Field>> {
    let row = first(c.query(&format!("SELECT l.fields FROM {} l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE l.id=$2 AND {VISIBLE}", logs(c)), &[json!(user),json!(id)])?, "log not found")?;
    serde_json::from_value(if row["fields"].is_null() {
        json!([])
    } else {
        row["fields"].clone()
    })
    .map_err(|_| AppError::internal("invalid stored field definitions"))
}

fn entry(c: &mut Conn, log: &str, id: &str) -> Result<Value> {
    let mut row = first(c.query(&format!("SELECT le.id,le.log_id,le.user_id,u.username,le.fields,le.occurred_at,le.created_at,le.updated_at,le.note FROM {} le JOIN users u ON u.id=le.user_id WHERE le.log_id=$1 AND le.id=$2", entries(c)), &[json!(log),json!(id)])?, "entry not found")?;
    null_fields(&mut row, json!({}));
    Ok(row)
}

fn list_entries(app: &App, user: &str, id: &str) -> Result<Value> {
    app.db.read(|c| {
        field_definitions(c, user, id)?;
        let mut rows = c.query(&format!("SELECT le.id,le.log_id,le.user_id,u.username,le.fields,le.occurred_at,le.created_at,le.updated_at,le.note FROM {} le JOIN users u ON u.id=le.user_id WHERE le.log_id=$1 ORDER BY le.occurred_at DESC", entries(c)), &[json!(id)])?;
        for row in &mut rows { null_fields(row, json!({})); }
        Ok(json!(rows))
    })
}

fn write_entry(
    app: &App,
    user: &str,
    log: &str,
    id: Option<&str>,
    input: EntryInput,
) -> Result<Value> {
    domain::note(input.note.as_deref())?;
    domain::id("log_id", log)?;
    domain::optional_id("entry_id", id)?;
    if id.is_some()
        && (input.occurred_at.is_none()
            || input.occurred_at.is_some_and(|time| {
                time.timestamp() == -62_135_596_800 && time.timestamp_subsec_nanos() == 0
            }))
    {
        return Err(AppError::bad_request("occurred_at is required"));
    }
    let fields = input.fields.unwrap_or_default();
    app.db.transaction(|c| {
        domain::values(&field_definitions(c, user, log)?, &fields)?;
        let entry_id = id.map(str::to_owned).unwrap_or_else(|| Uuid::now_v7().to_string());
        if id.is_some() {
            changed(c.execute(&format!("UPDATE {} SET fields=$1,occurred_at=$2,updated_at=now(),note=COALESCE($5,note) WHERE id=$3 AND log_id=$4", entries(c)), &[json!(fields),store::timestamp(input.occurred_at.unwrap()),json!(entry_id),json!(log),json!(input.note)])?, "entry not found")?;
        } else {
            let now = DateTime::from_timestamp_micros(Utc::now().timestamp_micros()).unwrap();
            c.execute(&format!("INSERT INTO {}(id,log_id,user_id,fields,occurred_at,note) VALUES($1,$2,$3,$4,$5,$6)", entries(c)), &[json!(entry_id),json!(log),json!(user),json!(fields),store::timestamp(now),json!(input.note.unwrap_or_default())])?;
        }
        entry(c, log, &entry_id)
    })
}

fn delete_entry(app: &App, user: &str, log: &str, id: &str) -> Result<()> {
    domain::id("log_id", log)?;
    domain::id("entry_id", id)?;
    app.db.transaction(|c| {
        field_definitions(c, user, log)?;
        changed(
            c.execute(
                &format!("DELETE FROM {} WHERE id=$1 AND log_id=$2", entries(c)),
                &[json!(id), json!(log)],
            )?,
            "entry not found",
        )
    })
}

fn owned_folder(c: &mut Conn, user: &str, id: &str, message: &str) -> Result<Value> {
    first(
        c.query(
            &format!("SELECT {FOLDER_COLUMNS} FROM folders WHERE id=$1 AND user_id=$2"),
            &[json!(id), json!(user)],
        )?,
        message,
    )
}

fn create_folder(app: &App, user: &str, input: FolderInput) -> Result<Value> {
    domain::optional_id("parent_folder_id", input.parent_folder_id.as_deref())?;
    let name = domain::name(input.name.as_deref().unwrap_or_default())?;
    app.db.transaction(|c| {
        if let Some(parent) = input.parent_folder_id.as_deref() {
            owned_folder(c, user, parent, "parent folder not found").map_err(|e| if e.status == 404 {AppError::bad_request(e.message)} else {e})?;
        }
        let parent = json!(input.parent_folder_id);
        let pos = first(c.query("SELECT COALESCE(max(position)+1,0) AS position FROM folders WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2", &[json!(user),parent.clone()])?, "folder not found")?;
        first(c.query(&format!("INSERT INTO folders(id,user_id,parent_folder_id,name,position) VALUES($1,$2,$3,$4,$5) RETURNING {FOLDER_COLUMNS}"), &[raw_id(),json!(user),parent,json!(name),pos["position"].clone()])?, "folder not found")
    })
}

fn rename_folder(app: &App, user: &str, id: &str, input: FolderInput) -> Result<Value> {
    domain::id("folder_id", id)?;
    let name = domain::name(input.name.as_deref().unwrap_or_default())?;
    app.db.transaction(|c| first(c.query(&format!("UPDATE folders SET name=$1,updated_at=now() WHERE id=$2 AND user_id=$3 RETURNING {FOLDER_COLUMNS}"), &[json!(name),json!(id),json!(user)])?, "folder not found"))
}

/// Shift the neighboring rows when a folder or placement is dragged.
/// All identifiers are internal constants, and all caller input is bound.
fn reorder(
    c: &mut Conn,
    user: &str,
    table: &str,
    parent_column: &str,
    source: (Value, i64),
    destination: (Value, i64),
) -> Result<i64> {
    let (old_parent, old) = source;
    let (new_parent, requested) = destination;
    let count = first(c.query(&format!("SELECT count(*) AS count FROM {table} WHERE user_id=$1 AND {parent_column} IS NOT DISTINCT FROM $2"), &[json!(user),new_parent.clone()])?, "position not found")?;
    let same = old_parent == new_parent;
    let pos = requested
        .max(0)
        .min(integer(&count, "count") - i64::from(same));
    if same {
        if pos > old {
            c.execute(&format!("UPDATE {table} SET position=position-1 WHERE user_id=$1 AND {parent_column} IS NOT DISTINCT FROM $2 AND position>$3 AND position<=$4"), &[json!(user),old_parent,json!(old),json!(pos)])?;
        } else if pos < old {
            c.execute(&format!("UPDATE {table} SET position=position+1 WHERE user_id=$1 AND {parent_column} IS NOT DISTINCT FROM $2 AND position>=$3 AND position<$4"), &[json!(user),old_parent,json!(pos),json!(old)])?;
        }
    } else {
        c.execute(&format!("UPDATE {table} SET position=position-1 WHERE user_id=$1 AND {parent_column} IS NOT DISTINCT FROM $2 AND position>$3"), &[json!(user),old_parent,json!(old)])?;
        c.execute(&format!("UPDATE {table} SET position=position+1 WHERE user_id=$1 AND {parent_column} IS NOT DISTINCT FROM $2 AND position>=$3"), &[json!(user),new_parent,json!(pos)])?;
    }
    Ok(pos)
}

fn locked(c: &mut Conn, sql: &str, params: &[Value], message: &str) -> Result<Value> {
    first(
        c.query_dialect(&format!("{sql} FOR UPDATE"), sql, params)?,
        message,
    )
}

fn move_folder(app: &App, user: &str, id: &str, input: FolderInput) -> Result<()> {
    domain::id("folder_id", id)?;
    domain::optional_id("parent_folder_id", input.parent_folder_id.as_deref())?;
    app.db.transaction(|c| {
        let row = locked(c, "SELECT user_id,parent_folder_id,position FROM folders WHERE id=$1 AND user_id=$2", &[json!(id),json!(user)], "folder not found")?;
        if let Some(parent) = input.parent_folder_id.as_deref() {
            if parent == id { return Err(AppError::bad_request("folder cannot be its own parent")); }
            owned_folder(c, user, parent, "parent folder not found").map_err(|e| if e.status == 404 {AppError::bad_request(e.message)} else {e})?;
            let cycle = first(c.query("WITH RECURSIVE descendants AS (SELECT id FROM folders WHERE id=$1 UNION ALL SELECT f.id FROM folders f JOIN descendants d ON f.parent_folder_id=d.id) SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$2) AS cycle", &[json!(id),json!(parent)])?, "folder not found")?;
            if cycle["cycle"] == true { return Err(AppError::bad_request("cannot move a folder into its own descendant")); }
        }
        let parent = json!(input.parent_folder_id);
        let pos = reorder(c, user, "folders", "parent_folder_id", (row["parent_folder_id"].clone(), integer(&row,"position")), (parent.clone(), input.position.unwrap_or(0)))?;
        c.execute("UPDATE folders SET parent_folder_id=$1,position=$2,updated_at=now() WHERE id=$3", &[parent,json!(pos),json!(id)])?;
        Ok(())
    })
}

fn delete_folder(app: &App, user: &str, id: &str) -> Result<()> {
    domain::id("folder_id", id)?;
    app.db.transaction(|c| {
        let row = locked(c, "SELECT parent_folder_id,position FROM folders WHERE id=$1 AND user_id=$2", &[json!(id),json!(user)], "folder not found")?;
        let children = first(c.query("SELECT EXISTS(SELECT 1 FROM folders WHERE parent_folder_id=$1) OR EXISTS(SELECT 1 FROM user_log_placements WHERE folder_id=$1) AS children", &[json!(id)])?, "folder not found")?;
        if children["children"] == true { return Err(AppError::conflict("folder is not empty")); }
        c.execute("DELETE FROM folders WHERE id=$1", &[json!(id)])?;
        c.execute("UPDATE folders SET position=position-1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>$3", &[json!(user),row["parent_folder_id"].clone(),row["position"].clone()])?;
        Ok(())
    })
}

fn placement(
    app: &App,
    user: &str,
    id: &str,
    operation: &str,
    input: PlacementInput,
) -> Result<()> {
    domain::id("log_id", id)?;
    if operation == "placement" {
        domain::optional_id("folder_id", input.folder_id.as_deref())?;
    }
    app.db.transaction(|c| {
        let row = locked(c, "SELECT folder_id,position,pinned_to_home,home_position FROM user_log_placements WHERE user_id=$1 AND log_id=$2", &[json!(user),json!(id)], "log not found")?;
        match operation {
            "placement" => {
                if let Some(folder) = input.folder_id.as_deref() {
                    owned_folder(c, user, folder, "folder not found").map_err(|e| if e.status == 404 {AppError::bad_request(e.message)} else {e})?;
                }
                let folder = json!(input.folder_id);
                let pos = reorder(c, user, "user_log_placements", "folder_id", (row["folder_id"].clone(), integer(&row,"position")), (folder.clone(), input.position.unwrap_or(0)))?;
                c.execute("UPDATE user_log_placements SET folder_id=$1,position=$2,updated_at=now() WHERE user_id=$3 AND log_id=$4", &[folder,json!(pos),json!(user),json!(id)])?;
            }
            "pin" => {
                let pinned = input.pinned.unwrap_or(false);
                if row["pinned_to_home"] == pinned { return Ok(()); }
                if pinned {
                    c.execute("UPDATE user_log_placements SET pinned_to_home=true,home_position=COALESCE((SELECT max(home_position)+1 FROM user_log_placements WHERE user_id=$1 AND pinned_to_home),0),updated_at=now() WHERE user_id=$1 AND log_id=$2", &[json!(user),json!(id)])?;
                } else {
                    c.execute("UPDATE user_log_placements SET pinned_to_home=false,updated_at=now() WHERE user_id=$1 AND log_id=$2", &[json!(user),json!(id)])?;
                }
            }
            "home-position" => {
                if row["pinned_to_home"] != true { return Err(AppError::bad_request("log is not pinned to home")); }
                let count = first(c.query("SELECT count(*) AS count FROM user_log_placements WHERE user_id=$1 AND pinned_to_home", &[json!(user)])?, "log not found")?;
                let pos = input.home_position.unwrap_or(0).max(0).min(integer(&count,"count")-1);
                let old = integer(&row,"home_position");
                if pos == old { return Ok(()); }
                if pos > old {
                    c.execute("UPDATE user_log_placements SET home_position=home_position-1 WHERE user_id=$1 AND pinned_to_home AND home_position>$2 AND home_position<=$3", &[json!(user),json!(old),json!(pos)])?;
                } else {
                    c.execute("UPDATE user_log_placements SET home_position=home_position+1 WHERE user_id=$1 AND pinned_to_home AND home_position>=$2 AND home_position<$3", &[json!(user),json!(pos),json!(old)])?;
                }
                c.execute("UPDATE user_log_placements SET home_position=$1,updated_at=now() WHERE user_id=$2 AND log_id=$3", &[json!(pos),json!(user),json!(id)])?;
            }
            _ => unreachable!(),
        }
        Ok(())
    })
}

fn share_token(app: &App, user: &str, id: &str, create: bool) -> Result<Value> {
    domain::id("log_id", id)?;
    let mut token = [0u8; 32];
    if create {
        use rand::TryRngCore;
        rand::rngs::OsRng
            .try_fill_bytes(&mut token)
            .map_err(AppError::internal)?;
    }
    app.db.transaction(|c| {
        let value = if create {
            store::bytes(&token)
        } else {
            Value::Null
        };
        changed(
            c.execute(
                &format!(
                    "UPDATE {} SET share_token=$1 WHERE id=$2 AND user_id=$3",
                    logs(c)
                ),
                &[value, json!(id), json!(user)],
            )?,
            "log not found",
        )?;
        Ok(json!({"share_token":hex::encode(token)}))
    })
}

fn decode_token(token: &str) -> Result<Value> {
    let bytes = hex::decode(token).map_err(|_| AppError::not_found("invalid share link"))?;
    if bytes.is_empty() {
        return Err(AppError::not_found("invalid share link"));
    }
    Ok(store::bytes(&bytes))
}

fn share_info(app: &App, user: &str, token: &str) -> Result<Value> {
    let token = decode_token(token)?;
    app.db.read(|c| {
        let mut info = first(c.query(&format!("SELECT l.id AS log_id,l.name AS log_name,u.username AS owner_username,l.user_id=$1 AS is_owner,EXISTS(SELECT 1 FROM log_shares ls WHERE ls.log_id=l.id AND ls.user_id=$1) AS already_member FROM {} l JOIN users u ON u.id=l.user_id WHERE l.share_token=$2", logs(c)), &[json!(user),token])?, "invalid share link")?;
        if info["is_owner"] == true { info["already_member"] = json!(false); }
        Ok(info)
    })
}

fn join(app: &App, user: &str, token: &str) -> Result<(Value, bool)> {
    let token = decode_token(token)?;
    app.db.transaction(|c| {
        let sql = format!("SELECT id,name,user_id FROM {} WHERE share_token=$1", logs(c));
        let row = first(c.query_dialect(&format!("{sql} FOR SHARE"), &sql.replace("FROM logs ", "FROM all_logs "), &[token])?, "invalid share link")?;
        if row["user_id"] == user { return Err(AppError::bad_request("you already own this log")); }
        let inserted = c.query("INSERT INTO log_shares(id,log_id,user_id) VALUES($1,$2,$3) ON CONFLICT (log_id,user_id) DO NOTHING RETURNING id", &[raw_id(),row["id"].clone(),json!(user)])?;
        let existing = inserted.is_empty();
        if !existing { create_placement(c, user, &row["id"])?; }
        Ok((json!({"log_id":row["id"],"log_name":row["name"]}),existing))
    })
}

pub fn list_saved_queries(app: &App, user: &str) -> Result<Value> {
    app.db.read(|c| Ok(json!(c.query(&format!("SELECT {SAVED_COLUMNS} FROM saved_sql_queries WHERE user_id=$1 ORDER BY lower(name)"), &[json!(user)])?)))
}

pub fn get_saved_query(app: &App, user: &str, name: &str) -> Result<Value> {
    app.db.read(|c| {
        first(
            c.query(
                &format!(
                    "SELECT {SAVED_COLUMNS} FROM saved_sql_queries WHERE user_id=$1 AND name=$2"
                ),
                &[json!(user), json!(name)],
            )?,
            "saved query not found",
        )
    })
}

fn write_saved(app: &App, user: &str, id: Option<&str>, input: SavedInput) -> Result<Value> {
    domain::optional_id("id", id)?;
    let name = domain::name(input.name.as_deref().unwrap_or_default())?;
    let text = input.query_text.unwrap_or_default();
    if text.trim().is_empty() {
        return Err(AppError::bad_request("query_text is required"));
    }
    if text.len() > 10_000 {
        return Err(AppError::bad_request("query_text is too long"));
    }
    app.db.transaction(|c| {
        let rows = if let Some(id) = id {
            c.query(&format!("UPDATE saved_sql_queries SET name=$1,query_text=$2,updated_at=now() WHERE id=$3 AND user_id=$4 RETURNING {SAVED_COLUMNS}"), &[json!(name),json!(text),json!(id),json!(user)])?
        } else {
            c.query(&format!("INSERT INTO saved_sql_queries(id,user_id,name,query_text) VALUES($1,$2,$3,$4) RETURNING {SAVED_COLUMNS}"), &[raw_id(),json!(user),json!(name),json!(text)])?
        };
        first(rows,"saved query not found")
    })
}

#[derive(Default, Deserialize, Serialize)]
struct Cursor {
    v: u8,
    collection: String,
    user: String,
    name: String,
    id: String,
}

fn decode_cursor(value: Option<&str>, collection: &str, user: &str) -> Result<Cursor> {
    let value = value.unwrap_or_default();
    if value.is_empty() {
        return Ok(Cursor {
            v: 1,
            collection: collection.into(),
            user: user.into(),
            ..Default::default()
        });
    }
    let invalid = || AppError::bad_request("invalid cursor");
    if value.len() > 1024 {
        return Err(invalid());
    }
    let raw = URL_SAFE_NO_PAD.decode(value).map_err(|_| invalid())?;
    let cursor: Cursor = serde_json::from_slice(&raw).map_err(|_| invalid())?;
    if cursor.v != 1
        || cursor.collection != collection
        || cursor.user != user
        || cursor.id.is_empty()
        || cursor.id.len() > 128
        || cursor.name.len() > 400
    {
        return Err(invalid());
    }
    Ok(cursor)
}

pub fn collection_page(
    app: &App,
    user: &str,
    collection: &str,
    cursor: Option<&str>,
    limit: usize,
) -> Result<Value> {
    let limit = if limit == 0 { 50 } else { limit };
    if limit > 100 {
        return Err(AppError::bad_request("limit must be between 1 and 100"));
    }
    if !matches!(collection, "logs" | "queries") {
        return Err(AppError::bad_request("invalid collection"));
    }
    let mut cursor = decode_cursor(cursor, collection, user)?;
    let rows = app.db.read_timeout(std::time::Duration::from_secs(5), |c| {
        let (columns, table, scope, prefix) = if collection == "logs" {
            ("l.id,l.name,l.fields,l.user_id=$1 AS is_owner,l.created_at,l.updated_at,lower(l.name) AS sort_name",format!("{} l",logs(c)),VISIBLE,"l.")
        } else {
            ("id,name,query_text,created_at,updated_at,lower(name) AS sort_name","saved_sql_queries".into(),"user_id=$1","")
        };
        let start = format!("SELECT {columns} FROM {table} WHERE ({scope})");
        let mut params = vec![json!(user)];
        let pg_filter;
        let jed_filter;
        if cursor.id.is_empty() { pg_filter=String::new(); jed_filter=String::new(); }
        else {
            pg_filter = format!(" AND (lower({prefix}name) COLLATE \"C\" > $2::text COLLATE \"C\" OR (lower({prefix}name) COLLATE \"C\" = $2::text COLLATE \"C\" AND {prefix}id::text COLLATE \"C\" > $3::text COLLATE \"C\"))");
            jed_filter = format!(" AND (lower({prefix}name) COLLATE \"C\" > $2 OR (lower({prefix}name) COLLATE \"C\" = $2 AND {prefix}id > $3))");
            params.extend([json!(cursor.name),json!(cursor.id)]);
        }
        let pg = format!("{start}{pg_filter} ORDER BY lower({prefix}name) COLLATE \"C\", {prefix}id::text COLLATE \"C\" LIMIT {}",limit+1);
        let jed = format!("{start}{jed_filter} ORDER BY lower({prefix}name) COLLATE \"C\", {prefix}id LIMIT {}",limit+1);
        c.query_dialect(&pg,&jed.replace("FROM logs l", "FROM all_logs l"),&params)
    })?;
    let all_len = rows.len();
    let mut selected = Vec::new();
    let mut size = 2048;
    for mut row in rows {
        if selected.len() == limit {
            break;
        }
        let sort_name = row
            .as_object_mut()
            .unwrap()
            .remove("sort_name")
            .unwrap_or_default();
        let encoded = serde_json::to_vec(&row)
            .map_err(|_| AppError::internal("cannot encode collection page"))?;
        if size + encoded.len() + 1 > 256 << 10 {
            if selected.is_empty() {
                return Err(AppError::bad_request(
                    "a record exceeds the collection page size limit",
                ));
            }
            break;
        }
        size += encoded.len() + 1;
        cursor.name = sort_name.as_str().unwrap_or_default().to_owned();
        cursor.id = row["id"].as_str().unwrap_or_default().to_owned();
        selected.push(row);
    }
    let mut result = json!({collection:selected});
    if result[collection].as_array().unwrap().len() < all_len {
        result["next_cursor"] = json!(
            URL_SAFE_NO_PAD.encode(
                serde_json::to_vec(&cursor)
                    .map_err(|_| AppError::internal("cannot encode collection cursor"))?
            )
        );
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cursor_is_bound_to_user_and_collection() {
        let cursor = Cursor {
            v: 1,
            collection: "logs".into(),
            user: "alice".into(),
            name: "water".into(),
            id: Uuid::now_v7().to_string(),
        };
        let token = URL_SAFE_NO_PAD.encode(serde_json::to_vec(&cursor).unwrap());
        assert!(decode_cursor(Some(&token), "logs", "alice").is_ok());
        assert!(decode_cursor(Some(&token), "queries", "alice").is_err());
        assert!(decode_cursor(Some(&token), "logs", "bob").is_err());
        assert!(decode_cursor(Some("not-base64!"), "logs", "alice").is_err());
    }
}

#[cfg(test)]
#[path = "catalog/tests.rs"]
mod integration_tests;
