//! The files of the session's workspace, for the people of the session to
//! look at and to add to:
//!
//!   GET /workspace?path=&after=&limit=    lists a directory
//!   GET /workspace/content?path=          reads a file, or a range of it
//!   PUT /workspace/content?path=          writes a file
//!   GET /workspace/search?query=&limit=   finds entries by their names
//!   GET /workspace/archive?path=          sends a directory as a ZIP file
//!
//! A path is relative to the workspace, and empty for the workspace itself.
//! What a link names is served only if it is in the workspace too.
//!
//! A request that is refused says why in the `code` of its answer:
//! `invalid_path`, `not_found`, `is_a_directory`, `not_a_directory`,
//! `not_permitted` or `no_room`.

use std::cmp::Ordering;
use std::env;
use std::fs::{self, File, Metadata};
use std::io::{self, BufWriter, Read, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::{Component, Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use axum::Json;
use axum::body::{Body, Bytes};
use axum::extract::{Query, Request};
use axum::http::{StatusCode, header};
use axum::response::{IntoResponse, Response};
use futures::{TryStreamExt, stream};
use serde::{Deserialize, Serialize};
use serde_json::json;
use time::{OffsetDateTime, PrimitiveDateTime};
use tokio::sync::mpsc;
use tokio::task;
use tokio_util::io::{StreamReader, SyncIoBridge};
use tower::ServiceExt;
use tower_http::services::ServeFile;
use walkdir::WalkDir;
use zip::result::ZipResult;
use zip::write::{FullFileOptions, SimpleFileOptions};
use zip::{CompressionMethod, DateTime, ZipWriter};

use crate::files;

/// The directory of the workspace, in the home.
const DIRECTORY: &str = "workspace";

/// The number of entries that a listing or a search returns unless it is
/// told another.
const LISTED: usize = 1000;

/// The number of entries that a search looks at before it gives up.
const LOOKED: usize = 200_000;

/// The number of entries that a search keeps to choose from.
const KEPT: usize = 10_000;

/// How much of an archive is passed on at a time, in bytes, and how many
/// such parts wait for whoever reads the archive.
const PART: usize = 64 << 10;
const PARTS: usize = 8;

/// A file of this size or more takes the form of a ZIP file that not every
/// reader knows: ZIP64.
const LARGE: u64 = u32::MAX as u64;

/// Why a request is refused.
#[derive(Debug)]
pub enum Refused {
    /// The path is not one below the workspace.
    InvalidPath,
    /// Nothing is at the path, or what is there is outside the workspace.
    NotFound,
    IsADirectory,
    NotADirectory,
    /// The runner may not read or write what is at the path.
    NotPermitted,
    /// The home has no room for the file.
    NoRoom,
    Failed(io::Error),
}

impl From<io::Error> for Refused {
    fn from(error: io::Error) -> Self {
        match error.kind() {
            io::ErrorKind::NotFound => Self::NotFound,
            io::ErrorKind::IsADirectory => Self::IsADirectory,
            // A directory that is to be made where a file is.
            io::ErrorKind::NotADirectory | io::ErrorKind::AlreadyExists => Self::NotADirectory,
            io::ErrorKind::PermissionDenied => Self::NotPermitted,
            io::ErrorKind::StorageFull | io::ErrorKind::QuotaExceeded => Self::NoRoom,
            io::ErrorKind::InvalidInput | io::ErrorKind::InvalidFilename => Self::InvalidPath,
            _ => Self::Failed(error),
        }
    }
}

impl IntoResponse for Refused {
    fn into_response(self) -> Response {
        let (status, code) = match &self {
            Self::InvalidPath => (StatusCode::BAD_REQUEST, "invalid_path"),
            Self::NotFound => (StatusCode::NOT_FOUND, "not_found"),
            Self::IsADirectory => (StatusCode::CONFLICT, "is_a_directory"),
            Self::NotADirectory => (StatusCode::CONFLICT, "not_a_directory"),
            Self::NotPermitted => (StatusCode::FORBIDDEN, "not_permitted"),
            Self::NoRoom => (StatusCode::INSUFFICIENT_STORAGE, "no_room"),
            Self::Failed(error) => {
                eprintln!("serve the workspace: {error}");
                (StatusCode::INTERNAL_SERVER_ERROR, "failed")
            }
        };
        (status, Json(json!({ "code": code }))).into_response()
    }
}

/// An entry of a directory.
#[derive(Serialize, Debug)]
struct Entry {
    name: String,
    /// "file", "directory" or "other". A link has the kind of what it names,
    /// or "symlink" if that is missing or outside the workspace.
    #[serde(rename = "type")]
    kind: &'static str,
    /// In bytes, of a file.
    #[serde(skip_serializing_if = "Option::is_none")]
    size: Option<u64>,
    /// When the entry was last changed, in milliseconds since the epoch.
    modified: u64,
    /// The path that a link names.
    #[serde(skip_serializing_if = "Option::is_none")]
    target: Option<String>,
}

/// Returns the workspace, with its links followed.
fn root() -> Result<PathBuf, Refused> {
    let home =
        env::var_os("HOME").ok_or_else(|| Refused::Failed(io::Error::other("HOME is not set")))?;
    Ok(fs::canonicalize(Path::new(&home).join(DIRECTORY))?)
}

/// Returns the path if it stays below the directory that it is relative to.
/// An empty path is that directory.
fn below(path: &str) -> Result<&Path, Refused> {
    let path = Path::new(path);
    let stays = path
        .components()
        .all(|part| matches!(part, Component::Normal(_)));
    stays.then_some(path).ok_or(Refused::InvalidPath)
}

/// Returns where the path leads in the workspace, with its links followed,
/// if that is in the workspace too.
fn find(root: &Path, path: &str) -> Result<PathBuf, Refused> {
    let found = fs::canonicalize(root.join(below(path)?))?;
    found
        .starts_with(root)
        .then_some(found)
        .ok_or(Refused::NotFound)
}

fn kind(of: &Metadata) -> &'static str {
    if of.is_dir() {
        "directory"
    } else if of.is_file() {
        "file"
    } else {
        "other"
    }
}

fn modified(of: &Metadata) -> u64 {
    of.modified()
        .ok()
        .and_then(|at| at.duration_since(UNIX_EPOCH).ok())
        .map_or(0, |since| since.as_millis() as u64)
}

/// Returns the entry of the name in the directory, if it is still there.
fn entry(root: &Path, directory: &Path, name: &str) -> Option<Entry> {
    let path = directory.join(name);
    let own = fs::symlink_metadata(&path).ok()?;
    if !own.file_type().is_symlink() {
        return Some(Entry {
            name: name.to_owned(),
            kind: kind(&own),
            size: own.is_file().then_some(own.len()),
            modified: modified(&own),
            target: None,
        });
    }
    let named = fs::canonicalize(&path)
        .ok()
        .filter(|found| found.starts_with(root))
        .and_then(|found| fs::metadata(found).ok());
    Some(Entry {
        name: name.to_owned(),
        kind: named.as_ref().map_or("symlink", kind),
        size: named
            .as_ref()
            .filter(|named| named.is_file())
            .map(Metadata::len),
        modified: modified(named.as_ref().unwrap_or(&own)),
        target: fs::read_link(&path)
            .ok()
            .map(|target| target.to_string_lossy().into_owned()),
    })
}

/// Where an entry stands in its directory: the directories first, and each
/// kind by name, whatever the case of its letters.
#[derive(PartialEq, Eq, PartialOrd, Ord)]
struct Place {
    file: bool,
    folded: String,
    name: String,
}

impl Place {
    fn of(file: bool, name: &str) -> Self {
        Self {
            file,
            folded: name.to_lowercase(),
            name: name.to_owned(),
        }
    }

    /// Returns the place that a listing gave as the one to go on after.
    fn after(given: &str) -> Result<Self, Refused> {
        match given.split_once('/') {
            Some(("d", name)) => Ok(Self::of(false, name)),
            Some(("f", name)) => Ok(Self::of(true, name)),
            _ => Err(Refused::InvalidPath),
        }
    }

    fn told(&self) -> String {
        format!("{}/{}", if self.file { "f" } else { "d" }, self.name)
    }
}

#[derive(Deserialize)]
pub struct Listed {
    #[serde(default)]
    path: String,
    /// The `next` of the listing before this one.
    #[serde(default)]
    after: String,
    limit: Option<usize>,
}

#[derive(Serialize)]
pub struct Listing {
    entries: Vec<Entry>,
    /// Where the listing ends, if the directory has more: a listing that is
    /// given it as `after` goes on from there.
    #[serde(skip_serializing_if = "Option::is_none")]
    next: Option<String>,
}

/// Lists a directory of the workspace: its directories first, and each kind
/// by name. A link is among what it names.
pub async fn list(Query(asked): Query<Listed>) -> Result<Json<Listing>, Refused> {
    blocking(move || listing(&asked)).await.map(Json)
}

fn listing(asked: &Listed) -> Result<Listing, Refused> {
    let root = root()?;
    let directory = find(&root, &asked.path)?;
    let after = match asked.after.as_str() {
        "" => None,
        given => Some(Place::after(given)?),
    };
    let mut places = Vec::new();
    for entry in fs::read_dir(&directory)? {
        let entry = entry?;
        // A name that is not text cannot be asked for.
        let Ok(name) = entry.file_name().into_string() else {
            continue;
        };
        let mut is_directory = entry.file_type()?.is_dir();
        if entry.file_type()?.is_symlink() {
            is_directory = fs::canonicalize(entry.path())
                .is_ok_and(|found| found.starts_with(&root) && found.is_dir());
        }
        let place = Place::of(!is_directory, &name);
        if after.as_ref().is_none_or(|after| place > *after) {
            places.push(place);
        }
    }
    places.sort();
    let limit = asked.limit.unwrap_or(LISTED).max(1);
    let next = (places.len() > limit).then(|| places[limit - 1].told());
    let entries = places
        .iter()
        .take(limit)
        .filter_map(|place| entry(&root, &directory, &place.name))
        .collect();
    Ok(Listing { entries, next })
}

#[derive(Deserialize)]
pub struct At {
    #[serde(default)]
    path: String,
}

/// Sends a file of the workspace, or the range of it that the request asks
/// for. What is neither a file nor a directory, such as a pipe, is not read:
/// opening it may never return.
pub async fn read(Query(asked): Query<At>, request: Request) -> Result<Response, Refused> {
    let file = blocking(move || {
        let found = find(&root()?, &asked.path)?;
        let of = fs::metadata(&found)?;
        if of.is_dir() {
            return Err(Refused::IsADirectory);
        }
        if !of.is_file() {
            return Err(Refused::NotPermitted);
        }
        Ok(found)
    })
    .await?;
    match ServeFile::new(file).oneshot(request).await {
        Ok(answer) => Ok(answer.map(Body::new)),
        Err(never) => match never {},
    }
}

/// Writes the body of the request as the file at the path, in place of the
/// one that is there, and answers with its entry: with 201 if there was
/// none. The directories above it that are missing are made.
pub async fn write(Query(asked): Query<At>, request: Request) -> Result<Response, Refused> {
    let body = request.into_body().into_data_stream();
    let file = SyncIoBridge::new(Box::pin(StreamReader::new(body.map_err(io::Error::other))));
    let (status, written) = blocking(move || put(&asked.path, file)).await?;
    Ok((status, Json(written)).into_response())
}

fn put(path: &str, file: impl Read) -> Result<(StatusCode, Entry), Refused> {
    let path = below(path)?;
    let name = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or(Refused::InvalidPath)?;
    let root = root()?;
    // The directory of the file, with its links followed as far as it is
    // there: what is missing of it is made where that leads.
    let mut directory = root.clone();
    for part in path.parent().into_iter().flat_map(Path::components) {
        directory.push(part);
        match fs::canonicalize(&directory) {
            Ok(found) if !found.starts_with(&root) => return Err(Refused::NotFound),
            Ok(found) if !found.is_dir() => return Err(Refused::NotADirectory),
            Ok(found) => directory = found,
            Err(error) if error.kind() == io::ErrorKind::NotFound => fs::create_dir(&directory)?,
            Err(error) => return Err(error.into()),
        }
    }
    let to = directory.join(name);
    let had = fs::symlink_metadata(&to).ok();
    if had.as_ref().is_some_and(Metadata::is_dir) {
        return Err(Refused::IsADirectory);
    }
    files::write(file, &to)?;
    let status = if had.is_some() {
        StatusCode::OK
    } else {
        StatusCode::CREATED
    };
    let written = entry(&root, &directory, name).ok_or(Refused::NotFound)?;
    Ok((status, written))
}

#[derive(Deserialize)]
pub struct Sought {
    query: String,
    limit: Option<usize>,
}

/// An entry that a search found, with its path in the workspace.
#[derive(Serialize)]
struct Found {
    path: String,
    #[serde(flatten)]
    entry: Entry,
}

#[derive(Serialize)]
pub struct Finding {
    entries: Vec<Found>,
    /// Whether the workspace has, or may have, more such entries.
    truncated: bool,
}

/// Finds the entries of the workspace whose names have the words of the
/// query, whatever the case of their letters. Those whose names begin with
/// them come first, and then those nearest to the workspace. The directories
/// that links name are not looked into.
pub async fn search(Query(asked): Query<Sought>) -> Result<Json<Finding>, Refused> {
    blocking(move || finding(&asked)).await.map(Json)
}

fn finding(asked: &Sought) -> Result<Finding, Refused> {
    let wanted = asked.query.to_lowercase();
    if wanted.is_empty() {
        return Err(Refused::InvalidPath);
    }
    let root = root()?;
    // What was found: whether its name begins otherwise, how deep it is, and
    // where.
    let mut found: Vec<(bool, usize, PathBuf)> = Vec::new();
    let mut truncated = false;
    let walk = WalkDir::new(&root).min_depth(1).into_iter();
    // A directory that cannot be read is passed over.
    for (looked, entry) in walk.filter_map(Result::ok).enumerate() {
        if looked == LOOKED || found.len() == KEPT {
            truncated = true;
            break;
        }
        let Some(name) = entry.file_name().to_str().map(str::to_lowercase) else {
            continue;
        };
        if name.contains(&wanted) {
            found.push((!name.starts_with(&wanted), entry.depth(), entry.into_path()));
        }
    }
    found.sort_by(|a, b| match (a.0, a.1).cmp(&(b.0, b.1)) {
        Ordering::Equal => a.2.cmp(&b.2),
        other => other,
    });
    let limit = asked.limit.unwrap_or(LISTED).max(1);
    truncated |= found.len() > limit;
    let entries = found
        .iter()
        .take(limit)
        .filter_map(|(_, _, path)| {
            let name = path.file_name()?.to_str()?;
            Some(Found {
                path: path.strip_prefix(&root).ok()?.to_str()?.to_owned(),
                entry: entry(&root, path.parent()?, name)?,
            })
        })
        .collect();
    Ok(Finding { entries, truncated })
}

/// Sends a directory of the workspace as a ZIP file, written while it is
/// sent. The paths in it begin with the name of the directory. A link is in
/// it as a link, and what cannot be read is left out.
pub async fn archive(Query(asked): Query<At>) -> Result<Response, Refused> {
    let (directory, name) = blocking(move || {
        let found = find(&root()?, &asked.path)?;
        if !fs::metadata(&found)?.is_dir() {
            return Err(Refused::NotADirectory);
        }
        let name = Path::new(&asked.path)
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or(DIRECTORY);
        Ok((found, name.to_owned()))
    })
    .await?;
    let (parts, sent) = mpsc::channel(PARTS);
    task::spawn_blocking(move || {
        let to = BufWriter::with_capacity(PART, Pipe(parts.clone()));
        if let Err(error) = pack(&directory, &name, to) {
            let why = error.to_string();
            // Whoever reads the archive is told that it is not whole, unless
            // it ended because they went away.
            if parts.blocking_send(Err(io::Error::other(error))).is_ok() {
                eprintln!("archive {}: {why}", directory.display());
            }
        }
    });
    let body = stream::unfold(sent, |mut sent| async {
        sent.recv().await.map(|part| (part, sent))
    });
    Ok((
        [(header::CONTENT_TYPE, "application/zip")],
        Body::from_stream(body),
    )
        .into_response())
}

/// Passes what is written on to whoever reads it, and waits while that
/// falls behind.
struct Pipe(mpsc::Sender<io::Result<Bytes>>);

impl Write for Pipe {
    fn write(&mut self, part: &[u8]) -> io::Result<usize> {
        self.0
            .blocking_send(Ok(Bytes::copy_from_slice(part)))
            .map_err(|_| io::Error::new(io::ErrorKind::BrokenPipe, "the reader is gone"))?;
        Ok(part.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

/// Writes the directory as a ZIP file, under the name.
fn pack(directory: &Path, name: &str, to: impl Write) -> ZipResult<()> {
    let mut archive = ZipWriter::new_stream(to);
    archive.add_directory(name, options(&fs::metadata(directory)?)?)?;
    let walk = WalkDir::new(directory).min_depth(1).sort_by_file_name();
    for entry in walk.into_iter().filter_map(Result::ok) {
        let (Ok(of), Some(path)) = (
            entry.metadata(),
            entry
                .path()
                .strip_prefix(directory)
                .ok()
                .and_then(Path::to_str),
        ) else {
            continue;
        };
        let path = format!("{name}/{path}");
        if of.is_dir() {
            archive.add_directory(path, options(&of)?)?;
        } else if of.is_symlink() {
            if let Ok(target) = fs::read_link(entry.path()) {
                archive.add_symlink(path, target.to_string_lossy(), options(&of)?)?;
            }
        } else if of.is_file() {
            let Ok(mut file) = File::open(entry.path()) else {
                continue;
            };
            archive.start_file(path, options(&of)?.large_file(of.len() >= LARGE))?;
            io::copy(&mut file, &mut archive)?;
        }
    }
    archive.finish()?.into_inner().flush()?;
    Ok(())
}

/// Returns how an entry is written into a ZIP file: compressed, with its
/// permissions, and with the time at which it was last changed. The date of
/// a ZIP file names no time zone, and is that of UTC here: a reader that
/// knows the other form of the time shows the right one.
fn options(of: &Metadata) -> ZipResult<FullFileOptions<'static, 'static>> {
    let changed = OffsetDateTime::from(of.modified().unwrap_or(SystemTime::now()));
    let date = PrimitiveDateTime::new(changed.date(), changed.time());
    let options = SimpleFileOptions::default()
        .compression_method(CompressionMethod::Deflated)
        .unix_permissions(of.permissions().mode())
        .last_modified_time(DateTime::try_from(date).unwrap_or_default());
    match u32::try_from(changed.unix_timestamp()) {
        Ok(seconds) => options.with_extended_timestamp(Some(seconds), None, None),
        Err(_) => Ok(options.into_full_options()),
    }
}

/// Runs what reads or writes files, which may take long, off the thread that
/// serves the requests.
async fn blocking<T: Send + 'static>(
    work: impl FnOnce() -> Result<T, Refused> + Send + 'static,
) -> Result<T, Refused> {
    task::spawn_blocking(work)
        .await
        .map_err(|panicked| Refused::Failed(io::Error::other(panicked)))?
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn path_stays_below_the_workspace() {
        for path in ["", "notes.txt", "src/main.rs", "보고서 (2).pdf", "a/b/c"] {
            assert_eq!(below(path).unwrap(), Path::new(path), "{path}");
        }
        for path in [
            "/",
            "..",
            "../notes.txt",
            "src/../../notes.txt",
            "/etc/passwd",
        ] {
            assert!(matches!(below(path), Err(Refused::InvalidPath)), "{path}");
        }
    }

    #[test]
    fn entries_are_in_the_order_of_their_places() {
        let mut places = [
            Place::of(true, "b.txt"),
            Place::of(true, "A.txt"),
            Place::of(false, "src"),
            Place::of(true, "a.txt"),
            Place::of(false, "Docs"),
        ];
        places.sort();
        let names: Vec<_> = places.iter().map(|place| place.name.as_str()).collect();
        assert_eq!(names, ["Docs", "src", "A.txt", "a.txt", "b.txt"]);
        // A listing goes on after the place that the one before it told.
        let after = Place::after(&places[2].told()).unwrap();
        let rest: Vec<_> = places.iter().filter(|place| **place > after).collect();
        assert_eq!(rest.len(), 2);
        assert!(Place::after("src").is_err());
    }
}
