//! Tests of the files of the workspace, as the people of a session read and
//! write them.

mod support;

use std::fs::{self, File, Permissions};
use std::io::{Cursor, Read};
use std::os::unix::fs::{PermissionsExt, symlink};
use std::os::unix::net::UnixListener;
use std::path::Path;
use std::time::{Duration, UNIX_EPOCH};

use reqwest::StatusCode;
use serde_json::{Value, json};
use zip::{DateTime, ZipArchive};

use support::{Home, runner, start};

/// Makes the files at the paths below the directory, each with its path as
/// its content, and the directories above them.
fn make(directory: &Path, paths: &[&str]) {
    for path in paths {
        let file = directory.join(path);
        fs::create_dir_all(file.parent().unwrap()).unwrap();
        fs::write(file, path).unwrap();
    }
}

/// Returns the values of the field of the entries of a listing.
fn fields<'a>(listing: &'a Value, field: &str) -> Vec<&'a str> {
    listing["entries"]
        .as_array()
        .unwrap()
        .iter()
        .map(|entry| entry[field].as_str().unwrap())
        .collect()
}

#[tokio::test(flavor = "multi_thread")]
async fn directory_lists_its_directories_first_and_each_kind_by_name() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    make(
        &workspace,
        &[
            "src/main.rs",
            "Docs/api.md",
            "b.txt",
            "A.txt",
            ".env",
            "a.txt",
        ],
    );
    let runner = start(runner(&home)).await;

    let (status, listing) = runner.asked("", &[]).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        fields(&listing, "name"),
        ["Docs", "src", ".env", "A.txt", "a.txt", "b.txt"]
    );
    assert_eq!(listing["next"], Value::Null);
    let entries = listing["entries"].as_array().unwrap();
    // A directory has no size.
    assert_eq!(entries[1]["type"], "directory");
    assert_eq!(entries[1].get("size"), None);
    assert_eq!(entries[5]["type"], "file");
    assert_eq!(entries[5]["size"], 5);
    assert!(entries[5]["modified"].as_u64().unwrap() > 1_700_000_000_000);

    let (_, listing) = runner.asked("", &[("path", "src")]).await;
    assert_eq!(fields(&listing, "name"), ["main.rs"]);

    // A listing of some of the entries tells where it ends, and the next
    // goes on from there.
    let (_, first) = runner.asked("", &[("limit", "2")]).await;
    assert_eq!(fields(&first, "name"), ["Docs", "src"]);
    let after = first["next"].as_str().unwrap();
    let (_, second) = runner.asked("", &[("limit", "3"), ("after", after)]).await;
    assert_eq!(fields(&second, "name"), [".env", "A.txt", "a.txt"]);
    let after = second["next"].as_str().unwrap();
    // Also if the entry that it ended with is gone.
    fs::remove_file(workspace.join("a.txt")).unwrap();
    let (_, last) = runner.asked("", &[("limit", "3"), ("after", after)]).await;
    assert_eq!(fields(&last, "name"), ["b.txt"]);
    assert_eq!(last["next"], Value::Null);
}

#[tokio::test(flavor = "multi_thread")]
async fn link_is_listed_as_what_it_names_in_the_workspace() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    make(&workspace, &["reports/notes.md"]);
    make(home.path(), &["outside/secret"]);
    symlink("reports", workspace.join("latest")).unwrap();
    symlink("reports/notes.md", workspace.join("notes")).unwrap();
    symlink("../outside", workspace.join("out")).unwrap();
    symlink("nowhere", workspace.join("gone")).unwrap();
    let runner = start(runner(&home)).await;

    let (_, listing) = runner.asked("", &[]).await;
    assert_eq!(
        fields(&listing, "name"),
        ["latest", "reports", "gone", "notes", "out"]
    );
    assert_eq!(
        fields(&listing, "type"),
        ["directory", "directory", "symlink", "file", "symlink"]
    );
    let entries = listing["entries"].as_array().unwrap();
    assert_eq!(entries[0]["target"], "reports");
    assert_eq!(entries[1].get("target"), None);
    assert_eq!(entries[3]["size"], 16);
    assert_eq!(entries[4]["target"], "../outside");

    // A link is followed where it stays in the workspace.
    let (_, through) = runner.asked("", &[("path", "latest")]).await;
    assert_eq!(fields(&through, "name"), ["notes.md"]);
    let (status, _, read) = runner.content("notes", None).await;
    assert_eq!(
        (status, read),
        (StatusCode::OK, b"reports/notes.md".to_vec())
    );
    // And nowhere else.
    for path in ["out", "gone"] {
        let (status, refused) = runner.asked("", &[("path", path)]).await;
        assert_eq!(status, StatusCode::NOT_FOUND, "{path}");
        assert_eq!(refused, json!({"code": "not_found"}), "{path}");
    }
    let (status, _, _) = runner.content("out/secret", None).await;
    assert_eq!(status, StatusCode::NOT_FOUND);
}

#[tokio::test(flavor = "multi_thread")]
async fn listing_is_refused_outside_the_workspace_and_of_what_is_no_directory() {
    let home = Home::new();
    make(&home.path().join("workspace"), &["a.txt"]);
    let runner = start(runner(&home)).await;
    for (path, status, code) in [
        ("..", StatusCode::BAD_REQUEST, "invalid_path"),
        ("/etc", StatusCode::BAD_REQUEST, "invalid_path"),
        ("src/../..", StatusCode::BAD_REQUEST, "invalid_path"),
        ("missing", StatusCode::NOT_FOUND, "not_found"),
        ("a.txt", StatusCode::CONFLICT, "not_a_directory"),
    ] {
        let (answered, refused) = runner.asked("", &[("path", path)]).await;
        assert_eq!(answered, status, "{path}");
        assert_eq!(refused, json!({"code": code}), "{path}");
    }
    // What a listing is told to go on after is what a listing told.
    let (answered, _) = runner.asked("", &[("after", "a.txt")]).await;
    assert_eq!(answered, StatusCode::BAD_REQUEST);
}

#[tokio::test(flavor = "multi_thread")]
async fn file_is_read_whole_or_a_range_of_it() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    let data: Vec<u8> = (0..25_600).map(|n| n as u8).collect();
    fs::write(workspace.join("data.bin"), &data).unwrap();
    make(&workspace, &["docs/보고서 (2).txt"]);
    let runner = start(runner(&home)).await;

    let (status, _, read) = runner.content("data.bin", None).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(read, data);
    let (status, told, read) = runner.content("data.bin", Some("bytes=10-19")).await;
    assert_eq!(status, StatusCode::PARTIAL_CONTENT);
    assert_eq!(told, "bytes 10-19/25600");
    assert_eq!(read, &data[10..20]);
    let (status, told, read) = runner.content("data.bin", Some("bytes=25590-")).await;
    assert_eq!(status, StatusCode::PARTIAL_CONTENT);
    assert_eq!(told, "bytes 25590-25599/25600");
    assert_eq!(read, &data[25_590..]);
    let (status, _, _) = runner.content("data.bin", Some("bytes=25600-")).await;
    assert_eq!(status, StatusCode::RANGE_NOT_SATISFIABLE);

    let (status, _, read) = runner.content("docs/보고서 (2).txt", None).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(read, "docs/보고서 (2).txt".as_bytes());

    // What is not a file is not opened.
    let _socket = UnixListener::bind(workspace.join("agent.sock")).unwrap();
    for (path, status, code) in [
        ("docs", StatusCode::CONFLICT, "is_a_directory"),
        ("", StatusCode::CONFLICT, "is_a_directory"),
        ("missing.txt", StatusCode::NOT_FOUND, "not_found"),
        ("data.bin/more", StatusCode::CONFLICT, "not_a_directory"),
        ("agent.sock", StatusCode::FORBIDDEN, "not_permitted"),
        (
            "../.aviary/harness-session",
            StatusCode::BAD_REQUEST,
            "invalid_path",
        ),
    ] {
        let (answered, _, refused) = runner.content(path, None).await;
        assert_eq!(answered, status, "{path}");
        let refused: Value = serde_json::from_slice(&refused).unwrap();
        assert_eq!(refused, json!({"code": code}), "{path}");
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn file_is_written_with_the_directories_above_it() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    let runner = start(runner(&home)).await;

    let (status, written) = runner.store("reports/2026/summary.md", "one").await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(written["name"], "summary.md");
    assert_eq!(written["type"], "file");
    assert_eq!(written["size"], 3);
    let file = workspace.join("reports/2026/summary.md");
    assert_eq!(fs::read_to_string(&file).unwrap(), "one");
    // A file that is there is replaced.
    let (status, written) = runner.store("reports/2026/summary.md", "two!").await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(written["size"], 4);
    assert_eq!(fs::read_to_string(&file).unwrap(), "two!");
    // A file is as large as it comes, and is there only when it is whole.
    let large = vec![b'a'; 5 << 20];
    let (status, written) = runner.store("reports/2026/large.bin", large.clone()).await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(written["size"], 5 << 20);
    assert_eq!(
        fs::read(workspace.join("reports/2026/large.bin")).unwrap(),
        large
    );
    let mut there: Vec<_> = fs::read_dir(workspace.join("reports/2026"))
        .unwrap()
        .map(|entry| entry.unwrap().file_name().into_string().unwrap())
        .collect();
    there.sort();
    assert_eq!(there, ["large.bin", "summary.md"]);

    for (path, status, code) in [
        ("reports", StatusCode::CONFLICT, "is_a_directory"),
        (
            "reports/2026/summary.md/x",
            StatusCode::CONFLICT,
            "not_a_directory",
        ),
        ("", StatusCode::BAD_REQUEST, "invalid_path"),
        ("../notes.txt", StatusCode::BAD_REQUEST, "invalid_path"),
        ("/etc/passwd", StatusCode::BAD_REQUEST, "invalid_path"),
    ] {
        let (answered, refused) = runner.store(path, "no").await;
        assert_eq!(answered, status, "{path}");
        assert_eq!(refused, json!({"code": code}), "{path}");
    }
    assert!(!home.path().join("notes.txt").exists());
}

#[tokio::test(flavor = "multi_thread")]
async fn file_is_not_written_through_a_link_that_leaves_the_workspace() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    make(&workspace, &["src/main.rs"]);
    make(home.path(), &["outside/secret"]);
    symlink("../outside", workspace.join("out")).unwrap();
    symlink("src", workspace.join("in")).unwrap();
    symlink("../outside/secret", workspace.join("named")).unwrap();
    let runner = start(runner(&home)).await;

    let (status, refused) = runner.store("out/x.txt", "no").await;
    assert_eq!(status, StatusCode::NOT_FOUND);
    assert_eq!(refused, json!({"code": "not_found"}));
    let (status, _) = runner.store("out/deeper/x.txt", "no").await;
    assert_eq!(status, StatusCode::NOT_FOUND);
    assert_eq!(
        fs::read_dir(home.path().join("outside")).unwrap().count(),
        1
    );
    // A link that stays in the workspace leads on.
    let (status, _) = runner.store("in/y.txt", "yes").await;
    assert_eq!(status, StatusCode::CREATED);
    assert_eq!(
        fs::read_to_string(workspace.join("src/y.txt")).unwrap(),
        "yes"
    );
    // A file takes the place of a link, and not that of what the link names.
    let (status, _) = runner.store("named", "mine").await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(fs::read_to_string(workspace.join("named")).unwrap(), "mine");
    assert!(!workspace.join("named").is_symlink());
    assert_eq!(
        fs::read_to_string(home.path().join("outside/secret")).unwrap(),
        "outside/secret"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn search_finds_entries_by_their_names() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    make(
        &workspace,
        &[
            "src/checkout/total.ts",
            "src/subtotal.ts",
            "test/checkout/total.test.ts",
            "docs/Totals.md",
            "totally/keep",
            "README.md",
        ],
    );
    // A link to a directory is not looked into, or the search would not end.
    symlink(".", workspace.join("src/again")).unwrap();
    let runner = start(runner(&home)).await;

    // Those whose names begin with the words come first, and then the
    // nearest to the workspace.
    let (status, found) = runner.asked("/search", &[("query", "TOTAL")]).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        fields(&found, "path"),
        [
            "totally",
            "docs/Totals.md",
            "src/checkout/total.ts",
            "test/checkout/total.test.ts",
            "src/subtotal.ts",
        ]
    );
    assert_eq!(found["truncated"], false);
    let entries = found["entries"].as_array().unwrap();
    assert_eq!(entries[0]["type"], "directory");
    assert_eq!(entries[1]["name"], "Totals.md");
    assert_eq!(entries[1]["size"], 14);

    let (_, some) = runner
        .asked("/search", &[("query", "total"), ("limit", "2")])
        .await;
    assert_eq!(fields(&some, "path"), ["totally", "docs/Totals.md"]);
    assert_eq!(some["truncated"], true);

    let (_, none) = runner.asked("/search", &[("query", "nothing")]).await;
    assert_eq!(none, json!({"entries": [], "truncated": false}));
    let (status, _) = runner.asked("/search", &[("query", "")]).await;
    assert_eq!(status, StatusCode::BAD_REQUEST);
}

#[tokio::test(flavor = "multi_thread")]
async fn directory_is_sent_as_a_zip_file() {
    let home = Home::new();
    let workspace = home.path().join("workspace");
    let reports = workspace.join("reports");
    make(
        &workspace,
        &[
            "reports/summary.md",
            "reports/보고서 (2).txt",
            "reports/run.sh",
            "README.md",
        ],
    );
    make(home.path(), &["outside/secret"]);
    fs::create_dir(reports.join("empty")).unwrap();
    fs::create_dir(reports.join("charts")).unwrap();
    // More than is passed on at a time, and nothing that gets smaller.
    let chart: Vec<u8> = (0..3_000_000u32)
        .map(|n| (n.wrapping_mul(2_654_435_761) >> 24) as u8)
        .collect();
    fs::write(reports.join("charts/a.png"), &chart).unwrap();
    fs::set_permissions(reports.join("run.sh"), Permissions::from_mode(0o755)).unwrap();
    symlink("summary.md", reports.join("latest")).unwrap();
    symlink("../../outside", reports.join("out")).unwrap();
    // 2026-01-02 03:04:06 UTC.
    let changed = UNIX_EPOCH + Duration::from_secs(1_767_323_046);
    File::options()
        .write(true)
        .open(reports.join("summary.md"))
        .unwrap()
        .set_modified(changed)
        .unwrap();
    let runner = start(runner(&home)).await;

    let (status, told, body) = runner.archive("reports").await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(told, "application/zip");
    let mut archive = ZipArchive::new(Cursor::new(body)).unwrap();
    let names: Vec<String> = archive
        .file_names()
        .map(|name| name.unwrap().into_owned())
        .collect();
    // The paths begin with the name of the directory. A directory that is
    // empty is there too, and a link is a link.
    assert_eq!(
        names,
        [
            "reports/",
            "reports/charts/",
            "reports/charts/a.png",
            "reports/empty/",
            "reports/latest",
            "reports/out",
            "reports/run.sh",
            "reports/summary.md",
            "reports/보고서 (2).txt",
        ]
    );
    let mut read = |name: &str| {
        let mut file = archive.by_name(name).unwrap();
        let mut content = Vec::new();
        file.read_to_end(&mut content).unwrap();
        (
            content,
            file.unix_mode().unwrap(),
            file.is_symlink(),
            file.last_modified(),
        )
    };
    assert_eq!(read("reports/charts/a.png").0, chart);
    assert_eq!(
        read("reports/보고서 (2).txt").0,
        "reports/보고서 (2).txt".as_bytes()
    );
    let (content, mode, _, at) = read("reports/summary.md");
    assert_eq!(content, b"reports/summary.md");
    assert_eq!(mode & 0o111, 0);
    assert_eq!(
        at,
        Some(DateTime::from_date_and_time(2026, 1, 2, 3, 4, 6).unwrap())
    );
    assert_eq!(read("reports/run.sh").1 & 0o777, 0o755);
    // What a link names is not in the archive, wherever it is.
    let (names, _, link, _) = read("reports/latest");
    assert_eq!((names.as_slice(), link), (&b"summary.md"[..], true));
    assert_eq!(read("reports/out").0, b"../../outside");

    // The workspace itself goes by its own name.
    let (status, _, body) = runner.archive("").await;
    assert_eq!(status, StatusCode::OK);
    let archive = ZipArchive::new(Cursor::new(body)).unwrap();
    let names: Vec<String> = archive
        .file_names()
        .map(|name| name.unwrap().into_owned())
        .collect();
    assert_eq!(&names[..2], ["workspace/", "workspace/README.md"]);
    assert_eq!(names.len(), 11);

    for (path, status, code) in [
        ("README.md", StatusCode::CONFLICT, "not_a_directory"),
        ("missing", StatusCode::NOT_FOUND, "not_found"),
        ("reports/out", StatusCode::NOT_FOUND, "not_found"),
        ("..", StatusCode::BAD_REQUEST, "invalid_path"),
    ] {
        let (answered, _, refused) = runner.archive(path).await;
        assert_eq!(answered, status, "{path}");
        let refused: Value = serde_json::from_slice(&refused).unwrap();
        assert_eq!(refused, json!({"code": code}), "{path}");
    }
}
