//! Tests of the health check of the runner, by which a sandbox is woken up.

mod support;

use reqwest::StatusCode;

use support::{Home, runner, start};

#[tokio::test(flavor = "multi_thread")]
async fn runner_answers_its_health_check_once_it_serves() {
    let home = Home::new();
    let runner = start(runner(&home)).await;
    assert_eq!(runner.healthy().await, StatusCode::OK);
}
