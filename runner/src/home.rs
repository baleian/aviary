//! The session's home as an archive: a tar file, compressed with zstd.
//!
//!   GET /home   packs the home
//!   PUT /home   unpacks an archive into the home
//!
//! A session whose sandbox is deleted keeps its home as such an archive, and
//! the runner of its next sandbox unpacks it before the first turn.

use std::collections::HashMap;
use std::collections::hash_map::Entry;
use std::env;
use std::fs;
use std::io::{self, BufWriter, Read, Write};
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::Ordering;

use axum::body::{Body, Bytes};
use axum::extract::{Request, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use futures::TryStreamExt;
use tar::{Archive, Builder, EntryType, Header, HeaderMode};
use tokio::sync::mpsc;
use tokio::task;
use tokio_util::io::{StreamReader, SyncIoBridge};

use crate::Runner;

/// The size of the parts in which a packed home is sent.
const PART: usize = 256 << 10;

/// Packs the home, unless a turn is running in it. A home that cannot be
/// packed whole ends the response without its end, so that the reader does
/// not take the archive for a whole one.
pub async fn send(State(runner): State<Arc<Runner>>) -> Response {
    if runner.running() {
        return refuse(StatusCode::CONFLICT, "a turn is running in the home");
    }
    let home = match home() {
        Ok(home) => home,
        Err(error) => return refuse(StatusCode::INTERNAL_SERVER_ERROR, error),
    };
    let (parts, mut packed) = mpsc::channel(4);
    task::spawn_blocking(move || {
        let into = BufWriter::with_capacity(PART, Parts(parts.clone()));
        if let Err(error) = pack(&home, into) {
            eprintln!("pack the home: {error}");
            let _ = parts.blocking_send(Err(error));
        }
    });
    let body = futures::stream::poll_fn(move |context| packed.poll_recv(context));
    Body::from_stream(body).into_response()
}

/// Unpacks the archive of the request into the home, over what is there. It
/// can be sent again if its outcome is not known, until the first turn: a
/// home that a turn has run in is not replaced.
pub async fn receive(State(runner): State<Arc<Runner>>, request: Request) -> Response {
    {
        // Under the lock, as a turn that starts checks the unpacking.
        let turn = runner.turn.lock().expect("turn lock poisoned");
        if turn.is_some() {
            return refuse(StatusCode::CONFLICT, "a turn has run in the home");
        }
        if runner.unpacking.swap(true, Ordering::SeqCst) {
            return refuse(StatusCode::CONFLICT, "the home is being unpacked");
        }
    }
    let unpacking = Unpacking(runner);
    let home = match home() {
        Ok(home) => home,
        Err(error) => return refuse(StatusCode::INTERNAL_SERVER_ERROR, error),
    };
    let body = request.into_body().into_data_stream();
    let archive = SyncIoBridge::new(Box::pin(StreamReader::new(body.map_err(io::Error::other))));
    // The unpacking ends with its task, also if the request is abandoned.
    let unpacked = task::spawn_blocking(move || {
        let _unpacking = unpacking;
        unpack(archive, &home)
    });
    match unpacked.await {
        Ok(Ok(())) => StatusCode::NO_CONTENT.into_response(),
        Ok(Err(error)) => {
            eprintln!("unpack the home: {error}");
            refuse(StatusCode::INTERNAL_SERVER_ERROR, error)
        }
        Err(panicked) => refuse(StatusCode::INTERNAL_SERVER_ERROR, panicked),
    }
}

/// Marks the end of an unpacking when it is dropped.
struct Unpacking(Arc<Runner>);

impl Drop for Unpacking {
    fn drop(&mut self) {
        self.0.unpacking.store(false, Ordering::SeqCst);
    }
}

fn refuse(status: StatusCode, why: impl ToString) -> Response {
    (status, why.to_string() + "\n").into_response()
}

fn home() -> io::Result<PathBuf> {
    env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| io::Error::other("HOME is not set"))
}

/// Sends what is written to it as the parts of a response body.
struct Parts(mpsc::Sender<io::Result<Bytes>>);

impl Write for Parts {
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

/// Writes the files of `home` to `into` as an archive: its directories,
/// files and symbolic links, with their modes and times. Files that are hard
/// links of each other stay so. Sockets and pipes are left out, since they
/// are of processes that end with the pod.
pub fn pack(home: &Path, into: impl Write) -> io::Result<()> {
    let mut compressed = zstd::Encoder::new(into, zstd::DEFAULT_COMPRESSION_LEVEL)?;
    // Lets unpack tell an archive that was damaged on the way.
    compressed.include_checksum(true)?;
    let mut archive = Builder::new(compressed);
    archive.follow_symlinks(false);
    archive.mode(HeaderMode::Complete);
    add(&mut archive, home, Path::new(""), &mut HashMap::new())?;
    archive.into_inner()?.finish()?.flush()
}

/// Adds the entries of `directory`, whose path in the archive is `name`.
/// `linked` has the first name of each file that has several.
fn add<W: Write>(
    archive: &mut Builder<W>,
    directory: &Path,
    name: &Path,
    linked: &mut HashMap<(u64, u64), PathBuf>,
) -> io::Result<()> {
    for entry in fs::read_dir(directory)? {
        let entry = entry?;
        let path = entry.path();
        let name = name.join(entry.file_name());
        // What a file system keeps of files that it has lost. The agent's
        // user cannot read it, and a new volume has its own.
        if name == Path::new("lost+found") {
            continue;
        }
        let metadata = entry.metadata()?;
        let kind = metadata.file_type();
        if kind.is_dir() {
            archive.append_dir(&name, &path)?;
            add(archive, &path, &name, linked)?;
        } else if kind.is_symlink() {
            archive.append_path_with_name(&path, &name)?;
        } else if kind.is_file() {
            let first = match linked.entry((metadata.dev(), metadata.ino())) {
                Entry::Occupied(first) => Some(first.get().clone()),
                Entry::Vacant(new) if metadata.nlink() > 1 => {
                    new.insert(name.clone());
                    None
                }
                Entry::Vacant(_) => None,
            };
            match first {
                Some(first) => {
                    let mut header = Header::new_gnu();
                    header.set_metadata_in_mode(&metadata, HeaderMode::Complete);
                    header.set_entry_type(EntryType::Link);
                    header.set_size(0);
                    archive.append_link(&mut header, &name, &first)?;
                }
                None => archive.append_path_with_name(&path, &name)?,
            }
        }
    }
    Ok(())
}

/// Unpacks the archive that `from` reads into `home`, over the files that are
/// there. It fails if the archive ends early or was damaged.
pub fn unpack(from: impl Read, home: &Path) -> io::Result<()> {
    let mut archive = Archive::new(zstd::Decoder::new(from)?);
    archive.set_preserve_permissions(true);
    archive.set_preserve_mtime(true);
    archive.unpack(home)?;
    // The end of the archive has its checksum, which is checked when it is
    // read.
    io::copy(&mut archive.into_inner(), &mut io::sink())?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::fs::{File, Permissions};
    use std::os::unix::fs::{PermissionsExt, symlink};
    use std::os::unix::net::UnixListener;
    use std::time::{Duration, SystemTime};

    use tempfile::TempDir;

    use super::*;

    fn packed(home: &Path) -> Vec<u8> {
        let mut archive = Vec::new();
        pack(home, &mut archive).unwrap();
        archive
    }

    /// Bytes that do not compress, so that an archive of them has their size.
    fn noise(length: usize) -> Vec<u8> {
        let mut state = 0x2545_f491_4f6c_dd1d_u64;
        (0..length)
            .map(|_| {
                state ^= state << 13;
                state ^= state >> 7;
                state ^= state << 17;
                state as u8
            })
            .collect()
    }

    /// Describes every entry under `root` by its path: its kind and mode, and
    /// for a file its time and content, for a link its target.
    fn tree(root: &Path) -> BTreeMap<PathBuf, String> {
        fn walk(root: &Path, directory: &Path, entries: &mut BTreeMap<PathBuf, String>) {
            for entry in fs::read_dir(directory).unwrap() {
                let path = entry.unwrap().path();
                let metadata = fs::symlink_metadata(&path).unwrap();
                let mode = metadata.mode() & 0o7777;
                let described = if metadata.is_dir() {
                    walk(root, &path, entries);
                    format!("directory {mode:o}")
                } else if metadata.file_type().is_symlink() {
                    format!("link to {}", fs::read_link(&path).unwrap().display())
                } else if metadata.is_file() {
                    let content = fs::read(&path).unwrap();
                    let shown = String::from_utf8_lossy(&content[..content.len().min(40)]);
                    let length = content.len();
                    let time = metadata.mtime();
                    format!("file {mode:o} of {length} bytes at {time}: {shown}")
                } else {
                    "neither a file nor a directory".to_owned()
                };
                entries.insert(path.strip_prefix(root).unwrap().to_owned(), described);
            }
        }
        let mut entries = BTreeMap::new();
        walk(root, root, &mut entries);
        entries
    }

    #[test]
    fn home_is_unpacked_as_it_was_packed() {
        let home = TempDir::new().unwrap();
        let at = |name: &str| home.path().join(name);
        fs::create_dir_all(at("workspace/src/deep")).unwrap();
        fs::create_dir(at("workspace/empty")).unwrap();
        fs::write(at("workspace/src/main.py"), "print('hello')\n").unwrap();
        fs::write(at("workspace/run.sh"), "#!/bin/sh\n").unwrap();
        fs::set_permissions(at("workspace/run.sh"), Permissions::from_mode(0o750)).unwrap();
        fs::write(at(".netrc"), "a password").unwrap();
        fs::set_permissions(at(".netrc"), Permissions::from_mode(0o600)).unwrap();
        fs::create_dir(at(".cache")).unwrap();
        fs::set_permissions(at(".cache"), Permissions::from_mode(0o700)).unwrap();
        fs::write(at(".cache/large"), noise(3 << 20)).unwrap();
        // Longer than the name of a tar header.
        let long = format!("workspace/src/deep/{}.txt", "긴이름-".repeat(20));
        fs::write(at(&long), "a long name").unwrap();
        let old = SystemTime::UNIX_EPOCH + Duration::from_secs(1_000_000_000);
        File::options()
            .write(true)
            .open(at("workspace/src/main.py"))
            .unwrap()
            .set_modified(old)
            .unwrap();
        symlink("src/main.py", at("workspace/main")).unwrap();
        symlink("/nowhere", at("workspace/dangling")).unwrap();
        fs::hard_link(at("workspace/src/main.py"), at("workspace/src/same.py")).unwrap();
        fs::hard_link(at("workspace/src/main.py"), at(".cache/same.py")).unwrap();
        fs::create_dir(at("workspace/lost+found")).unwrap();
        let expected = tree(home.path());
        assert_eq!(expected.len(), 15, "{expected:?}");

        // What an archive leaves out.
        let _socket = UnixListener::bind(at("workspace/agent.sock")).unwrap();
        fs::create_dir(at("lost+found")).unwrap();
        fs::write(at("lost+found/#1234"), "of the file system").unwrap();

        let unpacked = TempDir::new().unwrap();
        unpack(packed(home.path()).as_slice(), unpacked.path()).unwrap();
        assert_eq!(tree(unpacked.path()), expected);
        let inode = |name: &str| fs::metadata(unpacked.path().join(name)).unwrap().ino();
        assert_eq!(
            inode("workspace/src/main.py"),
            inode("workspace/src/same.py")
        );
        assert_eq!(inode("workspace/src/main.py"), inode(".cache/same.py"));
        assert_ne!(inode("workspace/src/main.py"), inode("workspace/run.sh"));
    }

    #[test]
    fn unpacking_replaces_the_files_of_the_archive_and_keeps_the_others() {
        let home = TempDir::new().unwrap();
        fs::create_dir(home.path().join("workspace")).unwrap();
        fs::write(home.path().join("workspace/notes"), "as packed").unwrap();
        symlink("notes", home.path().join("workspace/link")).unwrap();
        let archive = packed(home.path());

        // A new home has its workspace, and an unpacking that was cut short
        // has left files.
        let new = TempDir::new().unwrap();
        fs::create_dir(new.path().join("workspace")).unwrap();
        fs::write(new.path().join("workspace/notes"), "half of what was pa").unwrap();
        symlink("elsewhere", new.path().join("workspace/link")).unwrap();
        fs::write(new.path().join("workspace/other"), "not in the archive").unwrap();
        for _ in 0..2 {
            unpack(archive.as_slice(), new.path()).unwrap();
            let read = |name: &str| fs::read_to_string(new.path().join(name)).unwrap();
            assert_eq!(read("workspace/notes"), "as packed");
            assert_eq!(read("workspace/link"), "as packed");
            assert_eq!(read("workspace/other"), "not in the archive");
        }
    }

    #[test]
    fn archive_that_was_cut_or_damaged_is_not_unpacked_as_whole() {
        let home = TempDir::new().unwrap();
        fs::write(home.path().join("large"), noise(2 << 20)).unwrap();
        fs::write(home.path().join("small"), "a few bytes").unwrap();
        let archive = packed(home.path());
        assert!(archive.len() > 2 << 20, "{} bytes", archive.len());

        for length in [
            0,
            1,
            archive.len() / 2,
            archive.len() - 5,
            archive.len() - 1,
        ] {
            let into = TempDir::new().unwrap();
            let unpacked = unpack(&archive[..length], into.path());
            assert!(unpacked.is_err(), "the first {length} bytes were unpacked");
        }
        for at in [archive.len() / 3, archive.len() - 2] {
            let mut damaged = archive.clone();
            damaged[at] ^= 0x55;
            let into = TempDir::new().unwrap();
            let unpacked = unpack(damaged.as_slice(), into.path());
            assert!(unpacked.is_err(), "an archive damaged at {at} was unpacked");
        }
        let into = TempDir::new().unwrap();
        assert!(unpack(&b"not an archive"[..], into.path()).is_err());
    }

    #[test]
    fn home_with_a_file_that_cannot_be_read_is_not_packed() {
        let home = TempDir::new().unwrap();
        fs::write(home.path().join("readable"), "one").unwrap();
        fs::write(home.path().join("unreadable"), "another").unwrap();
        fs::set_permissions(
            home.path().join("unreadable"),
            Permissions::from_mode(0o000),
        )
        .unwrap();
        if fs::read(home.path().join("unreadable")).is_ok() {
            // Root reads every file.
            return;
        }
        let packed = pack(home.path(), Vec::new());
        assert!(
            packed.is_err(),
            "a home was packed without one of its files"
        );
    }
}
