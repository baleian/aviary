// Generates the client of the runner protocol from proto/. The .proto files
// are compiled by protox, so the build needs no protoc.
fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo::rerun-if-changed=../proto");
    let files = protox::compile(["aviary/runner/v1/runner.proto"], ["../proto"])?;
    tonic_prost_build::configure()
        .build_server(false)
        .compile_fds(files)?;
    Ok(())
}
