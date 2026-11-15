// Generates the client of the runner protocol from proto/, and its server for
// the tests, which stand in for the Supervisor. The .proto files are compiled
// by protox, so the build needs no protoc.
fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo::rerun-if-changed=../proto");
    let files = protox::compile(["aviary/runner/v1/runner.proto"], ["../proto"])?;
    tonic_prost_build::configure()
        .build_server(false)
        .compile_fds(files.clone())?;
    let supervisor = std::path::Path::new(&std::env::var("OUT_DIR")?).join("supervisor");
    std::fs::create_dir_all(&supervisor)?;
    tonic_prost_build::configure()
        .build_client(false)
        .out_dir(supervisor)
        .compile_fds(files)?;
    Ok(())
}
