use super::*;
use crate::Config;

struct Fixture {
    app: App,
    _dir: tempfile::TempDir,
}
impl Fixture {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        let app = App::open(Config {
            database_backend: std::env::var("TEST_DATABASE_BACKEND")
                .unwrap_or_else(|_| "jed".into()),
            database_url: std::env::var("TEST_DATABASE_URL").unwrap_or_default(),
            jed_data_dir: dir.path().to_string_lossy().into(),
            ..Config::default()
        })
        .unwrap();
        Self { app, _dir: dir }
    }
    fn user(&self, username: &str) -> String {
        let id = Uuid::new_v4().to_string();
        self.app
            .db
            .transaction(|c| {
                c.execute(
                    "INSERT INTO users(id,username,password_hash) VALUES($1,$2,$3)",
                    &[
                        json!(id),
                        json!(format!("{username}_{}", &id[..8])),
                        json!("test hash"),
                    ],
                )
            })
            .unwrap();
        id
    }
    fn request(
        &self,
        user: Option<&str>,
        method: &str,
        path: &str,
        body: Value,
    ) -> Result<Response> {
        let req = Request {
            method: method.into(),
            path: path.into(),
            query: Default::default(),
            headers: Default::default(),
            body: serde_json::to_vec(&body).unwrap(),
            user: user.map(|id| json!({"id":id})),
            remote_ip: "127.0.0.1".parse().unwrap(),
        };
        handle(&self.app, &req).expect("catalog route")
    }
    fn json(&self, user: &str, method: &str, path: &str, body: Value, status: u16) -> Value {
        let response = self.request(Some(user), method, path, body).unwrap();
        assert_eq!(response.status, status, "{method} {path}");
        if response.body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&response.body).unwrap()
        }
    }
}

#[test]
fn catalog_jed_sharing_entries_and_revoked_membership() {
    let f = Fixture::new();
    let owner = f.user("owner");
    let member = f.user("member");
    let anonymous = f
        .request(None, "POST", "/api/logs", json!({"name":""}))
        .err()
        .unwrap();
    assert_eq!(anonymous.status, 401);
    let log = f.json(
        &owner,
        "POST",
        "/api/logs",
        json!({"name":" Vitamins ","fields":[{"name":"dose","type":"number","required":true}]}),
        201,
    );
    assert_eq!(log["name"], "Vitamins");
    assert_eq!(log["pinned_to_home"], true);
    let path = format!("/api/logs/{}", log["id"].as_str().unwrap());
    assert_eq!(
        f.request(Some(&member), "GET", &path, Value::Null)
            .err()
            .unwrap()
            .status,
        404
    );
    assert_eq!(
        f.request(
            Some(&owner),
            "POST",
            "/api/logs",
            json!({"name":"VITAMINS"})
        )
        .err()
        .unwrap()
        .status,
        409
    );
    let share = f.json(
        &owner,
        "POST",
        &format!("{path}/share-token"),
        Value::Null,
        200,
    );
    let token = share["share_token"].as_str().unwrap();
    assert_eq!(token.len(), 64);
    let join_path = format!("/api/join/{token}");
    f.json(&member, "POST", &join_path, Value::Null, 201);
    f.json(&member, "POST", &join_path, Value::Null, 200);
    let visible = f.json(&member, "GET", &path, Value::Null, 200);
    assert_eq!(visible["is_owner"], false);
    assert!(visible.get("share_token").is_none());
    assert_eq!(
        f.request(Some(&member), "PUT", &path, json!({"name":"Hijacked"}))
            .err()
            .unwrap()
            .status,
        404
    );
    let entry_path = format!("{path}/entries");
    assert_eq!(
        f.request(
            Some(&member),
            "POST",
            &entry_path,
            json!({"fields":{"dose":500}})
        )
        .err()
        .unwrap()
        .status,
        400
    );
    let entry = f.json(
        &member,
        "POST",
        &entry_path,
        json!({"fields":{"dose":"500"},"note":"# Better 🙂"}),
        201,
    );
    assert!(entry["username"].as_str().unwrap().starts_with("member_"));
    let specific = format!("{entry_path}/{}", entry["id"].as_str().unwrap());
    let updated = f.json(
        &owner,
        "PUT",
        &specific,
        json!({"fields":{"dose":"250"},"occurred_at":entry["occurred_at"]}),
        200,
    );
    assert_eq!(updated["note"], entry["note"]);
    assert_eq!(updated["user_id"], member);
    let updated = f.json(
        &owner,
        "PUT",
        &specific,
        json!({"fields":{"dose":"250"},"occurred_at":entry["occurred_at"],"note":""}),
        200,
    );
    assert_eq!(updated["note"], "");
    let folder = f.json(
        &member,
        "POST",
        "/api/folders",
        json!({"name":"Shared"}),
        201,
    );
    f.json(
        &member,
        "PUT",
        &format!("{path}/placement"),
        json!({"folder_id":folder["id"],"position":0}),
        204,
    );
    let shares = f.json(&owner, "GET", &format!("{path}/shares"), Value::Null, 200);
    f.json(
        &owner,
        "DELETE",
        &format!("{path}/shares/{}", shares[0]["id"].as_str().unwrap()),
        Value::Null,
        204,
    );
    for (method, p, body) in [
        ("GET", path.clone(), Value::Null),
        ("GET", entry_path.clone(), Value::Null),
        ("DELETE", specific.clone(), Value::Null),
        (
            "PUT",
            specific,
            json!({"fields":{"dose":"5"},"occurred_at":entry["occurred_at"]}),
        ),
    ] {
        assert_eq!(
            f.request(Some(&member), method, &p, body)
                .err()
                .unwrap()
                .status,
            404
        );
    }
    assert_eq!(list_logs(&f.app, &member).unwrap(), json!([]));
    assert_eq!(
        f.json(&owner, "GET", &entry_path, Value::Null, 200)
            .as_array()
            .unwrap()
            .len(),
        1
    );
    f.json(&member, "POST", &join_path, Value::Null, 201);
    assert_eq!(
        f.json(&member, "GET", &path, Value::Null, 200)["folder_id"],
        folder["id"]
    );
    f.json(&owner, "DELETE", &path, Value::Null, 204);
    assert_eq!(
        f.request(Some(&member), "GET", &entry_path, Value::Null)
            .err()
            .unwrap()
            .status,
        404
    );
}

#[test]
fn catalog_jed_folder_cycles_reordering_and_pins() {
    let f = Fixture::new();
    let user = f.user("organizer");
    let a = f.json(&user, "POST", "/api/folders", json!({"name":"A"}), 201);
    let b = f.json(&user, "POST", "/api/folders", json!({"name":"B"}), 201);
    let child = f.json(
        &user,
        "POST",
        "/api/folders",
        json!({"name":"Child","parent_folder_id":a["id"]}),
        201,
    );
    let ap = format!("/api/folders/{}", a["id"].as_str().unwrap());
    assert_eq!(
        f.request(
            Some(&user),
            "PUT",
            &format!("{ap}/move"),
            json!({"parent_folder_id":child["id"]})
        )
        .err()
        .unwrap()
        .message,
        "cannot move a folder into its own descendant"
    );
    assert_eq!(
        f.request(
            Some(&user),
            "PUT",
            &format!("{ap}/move"),
            json!({"parent_folder_id":a["id"]})
        )
        .err()
        .unwrap()
        .message,
        "folder cannot be its own parent"
    );
    assert_eq!(
        f.request(Some(&user), "DELETE", &ap, Value::Null)
            .err()
            .unwrap()
            .status,
        409
    );
    f.json(
        &user,
        "PUT",
        &format!("/api/folders/{}/move", b["id"].as_str().unwrap()),
        json!({"position":-2}),
        204,
    );
    let folders = f.json(&user, "GET", "/api/folders", Value::Null, 200);
    assert_eq!(folders[0]["id"], b["id"]);
    let mut log_ids = Vec::new();
    for name in ["First", "Second", "Third"] {
        log_ids.push(
            f.json(&user, "POST", "/api/logs", json!({"name":name}), 201)["id"]
                .as_str()
                .unwrap()
                .to_owned(),
        );
    }
    f.json(
        &user,
        "PUT",
        &format!("/api/logs/{}/placement", log_ids[2]),
        json!({"position":0}),
        204,
    );
    let logs = list_logs(&f.app, &user).unwrap();
    assert_eq!(logs[0]["id"], log_ids[2]);
    for (i, log) in logs.as_array().unwrap().iter().enumerate() {
        assert_eq!(log["position"], i);
    }
    f.json(
        &user,
        "PUT",
        &format!("/api/logs/{}/home-position", log_ids[0]),
        json!({"home_position":99}),
        204,
    );
    let log = f.json(
        &user,
        "GET",
        &format!("/api/logs/{}", log_ids[0]),
        Value::Null,
        200,
    );
    assert_eq!(log["home_position"], 2);
    f.json(
        &user,
        "PUT",
        &format!("/api/logs/{}/pin", log_ids[0]),
        json!({"pinned":false}),
        204,
    );
    assert_eq!(
        f.request(
            Some(&user),
            "PUT",
            &format!("/api/logs/{}/home-position", log_ids[0]),
            json!({"home_position":0})
        )
        .err()
        .unwrap()
        .status,
        400
    );
}

#[test]
fn catalog_jed_pages_saved_queries_and_persistence() {
    let f = Fixture::new();
    let user = f.user("pages");
    for name in ["Zulu", "alpha", "Beta"] {
        f.json(&user, "POST", "/api/logs", json!({"name":name}), 201);
    }
    let page = collection_page(&f.app, &user, "logs", None, 1).unwrap();
    assert_eq!(page["logs"][0]["name"], "alpha");
    assert!(page["logs"][0].get("folder_id").is_none());
    let next = collection_page(&f.app, &user, "logs", page["next_cursor"].as_str(), 1).unwrap();
    assert_eq!(next["logs"][0]["name"], "Beta");
    let last = collection_page(&f.app, &user, "logs", next["next_cursor"].as_str(), 1).unwrap();
    assert_eq!(last["logs"][0]["name"], "Zulu");
    assert!(last.get("next_cursor").is_none());
    for name in ["alpha", "Alpha"] {
        f.json(
            &user,
            "POST",
            "/api/sql/saved",
            json!({"name":name,"query_text":"SELECT 1"}),
            201,
        );
    }
    assert_eq!(
        f.request(
            Some(&user),
            "POST",
            "/api/sql/saved",
            json!({"name":"alpha","query_text":"SELECT 2"})
        )
        .err()
        .unwrap()
        .status,
        409
    );
    assert_eq!(
        list_saved_queries(&f.app, &user)
            .unwrap()
            .as_array()
            .unwrap()
            .len(),
        2
    );
    assert_eq!(
        get_saved_query(&f.app, &user, "Alpha").unwrap()["query_text"],
        "SELECT 1"
    );
    // A bounded page must resume at its last emitted record, including when bytes
    // rather than item count shorten the page.
    for i in 0..35 {
        f.json(
            &user,
            "POST",
            "/api/sql/saved",
            json!({"name":format!("query{i:02}"),"query_text":"x".repeat(10_000)}),
            201,
        );
    }
    let page = collection_page(&f.app, &user, "queries", None, 100).unwrap();
    assert!(serde_json::to_vec(&page).unwrap().len() < 256 << 10);
    assert!(page.get("next_cursor").is_some());
    let next =
        collection_page(&f.app, &user, "queries", page["next_cursor"].as_str(), 100).unwrap();
    assert_eq!(
        page["queries"].as_array().unwrap().len() + next["queries"].as_array().unwrap().len(),
        37
    );
    assert!(next.get("next_cursor").is_none());
    let config = f.app.config.clone();
    let first_ids = list_logs(&f.app, &user).unwrap();
    drop(f.app);
    let reopened = App::open(config).unwrap();
    assert_eq!(list_logs(&reopened, &user).unwrap(), first_ids);
}
