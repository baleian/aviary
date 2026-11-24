//! The files that the messages of the session carry, which are written into
//! the home before the turn of their message:
//!
//!   PUT /files/{path}   writes a file
//!
//! They are under `.attachments` in the home, where the agent reads them, and
//! stay there for the turns that follow.

use std::env;
use std::fs::{self, File};
use std::io::{self, BufWriter, Read, Write};
use std::path::{Component, Path, PathBuf};
use std::process;

use axum::extract::{Path as Named, Request};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use futures::TryStreamExt;
use tokio::task;
use tokio_util::io::{StreamReader, SyncIoBridge};

/// The directory of the files, in the home.
const DIRECTORY: &str = ".attachments";

/// Writes the body of the request as the file of the path, which is relative
/// to the directory of the files. A file that is there is replaced, so the
/// request can be sent again. A volume without room for the file is reported
/// as such.
pub async fn receive(Named(path): Named<String>, request: Request) -> Response {
    let Some(path) = below(&path) else {
        return refuse(
            StatusCode::BAD_REQUEST,
            "the path names no file below the directory of the files",
        );
    };
    let Some(home) = env::var_os("HOME") else {
        return refuse(StatusCode::INTERNAL_SERVER_ERROR, "HOME is not set");
    };
    let to = PathBuf::from(home).join(DIRECTORY).join(path);
    let body = request.into_body().into_data_stream();
    let file = SyncIoBridge::new(Box::pin(StreamReader::new(body.map_err(io::Error::other))));
    match task::spawn_blocking(move || write(file, &to)).await {
        Ok(Ok(())) => StatusCode::NO_CONTENT.into_response(),
        Ok(Err(error)) => {
            eprintln!("write a file: {error}");
            let full = matches!(
                error.kind(),
                io::ErrorKind::StorageFull | io::ErrorKind::QuotaExceeded
            );
            let status = if full {
                StatusCode::INSUFFICIENT_STORAGE
            } else {
                StatusCode::INTERNAL_SERVER_ERROR
            };
            refuse(status, error)
        }
        Err(panicked) => refuse(StatusCode::INTERNAL_SERVER_ERROR, panicked),
    }
}

fn refuse(status: StatusCode, why: impl ToString) -> Response {
    (status, why.to_string() + "\n").into_response()
}

/// Returns the path if it stays below the directory that it is relative to,
/// and names a file there.
fn below(path: &str) -> Option<&Path> {
    let path = Path::new(path);
    let stays = path
        .components()
        .all(|part| matches!(part, Component::Normal(_)));
    (stays && path.file_name().is_some()).then_some(path)
}

/// Writes what is read from `file` as the file at `to`. The file gets its
/// name only when it is whole, so that the agent never reads a part of it.
fn write(mut file: impl Read, to: &Path) -> io::Result<()> {
    let (Some(directory), Some(name)) = (to.parent(), to.file_name()) else {
        return Err(io::Error::other("the path names no file"));
    };
    fs::create_dir_all(directory)?;
    let part = directory.join(format!(
        ".{}.{}.part",
        name.to_string_lossy(),
        process::id()
    ));
    let written = File::create(&part).and_then(|created| {
        let mut into = BufWriter::new(created);
        io::copy(&mut file, &mut into)?;
        into.flush()
    });
    let named = written.and_then(|()| fs::rename(&part, to));
    if named.is_err() {
        // Not the error of the file, which says more.
        let _ = fs::remove_file(&part);
    }
    named
}

#[cfg(test)]
mod tests {
    use tempfile::TempDir;

    use super::*;

    #[test]
    fn path_stays_below_the_directory() {
        for path in ["notes.txt", "3/notes.txt", "3/보고서 2026.pdf", "a/b/c"] {
            assert_eq!(below(path), Some(Path::new(path)), "{path}");
        }
        for path in [
            "",
            "/",
            "..",
            "../notes.txt",
            "3/../../notes.txt",
            "/etc/passwd",
        ] {
            assert_eq!(below(path), None, "{path}");
        }
    }

    #[test]
    fn file_is_written_whole_and_replaces_the_one_that_is_there() {
        let directory = TempDir::new().unwrap();
        let to = directory.path().join("3/notes.txt");
        write(&b"first"[..], &to).unwrap();
        assert_eq!(fs::read(&to).unwrap(), b"first");
        write(&b"second, and longer"[..], &to).unwrap();
        assert_eq!(fs::read(&to).unwrap(), b"second, and longer");

        // A file that does not arrive whole leaves the one that is there, and
        // nothing beside it.
        struct Cut;
        impl Read for Cut {
            fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
                Err(io::Error::other("the sender is gone"))
            }
        }
        assert!(write(Cut, &to).is_err());
        assert_eq!(fs::read(&to).unwrap(), b"second, and longer");
        let beside: Vec<_> = fs::read_dir(to.parent().unwrap())
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(beside, ["notes.txt"]);
    }
}
