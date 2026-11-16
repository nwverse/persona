//! DEBUG-only answer projections with a bounded native wait.
//!
//! One detached Rust worker owns at most the job it is currently evaluating. The JNI caller waits at most one second;
//! a busy worker rejects a new job instead of retaining another question or custom answer in an unbounded queue.
//! No job, result or error is logged. The worker never calls back into Java.

use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
use std::sync::{Mutex, OnceLock};
use std::time::Duration;

pub(crate) enum Operation {
    Project,
    Display,
}

struct Job {
    operation: Operation,
    input: String,
    result: SyncSender<Option<String>>,
}

static WORKER: OnceLock<Mutex<SyncSender<Job>>> = OnceLock::new();

fn worker() -> &'static Mutex<SyncSender<Job>> {
    WORKER.get_or_init(|| {
        let (sender, receiver) = sync_channel::<Job>(1);
        std::thread::Builder::new()
            .name("persona-answer-shadow".to_owned())
            .spawn(move || run(receiver))
            .expect("the DEBUG answer worker must start");
        Mutex::new(sender)
    })
}

fn run(receiver: Receiver<Job>) {
    while let Ok(job) = receiver.recv() {
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| match job.operation {
            Operation::Project => project(&job.input),
            Operation::Display => display(&job.input),
        }))
        .ok()
        .flatten();
        // A timed-out caller has dropped its receiver. Dropping the result here releases its text as well.
        let _ = job.result.send(result);
    }
}

fn project(input: &str) -> Option<String> {
    let input: persona_core_domain::answer::Input = serde_json::from_str(input).ok()?;
    let transition = persona_core_domain::answer::project(&input)?;
    serde_json::to_string(&transition).ok()
}

fn display(input: &str) -> Option<String> {
    let display = persona_core_domain::answer::display(input);
    serde_json::to_string(&display).ok()
}

/// A full worker or a slow native projection is a diagnostic failure, not a reason to retain another input.
pub(crate) fn call(operation: Operation, input: String) -> Option<String> {
    let (sender, receiver) = sync_channel(0);
    let job = Job {
        operation,
        input,
        result: sender,
    };
    // `try_send` never queues behind an already busy native projection.
    worker().lock().ok()?.try_send(job).ok()?;
    receiver.recv_timeout(Duration::from_secs(1)).ok().flatten()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn real_worker_projects_and_displays_without_retaining_an_input_queue() {
        let input = r#"{"interaction":{"kind":"question","questions":[{"question":"One","options":[{"label":"A"}]}]},"operation":{"action":"pick_option","page":0,"picks":{},"other":{},"sending":false,"option_index":0}}"#.to_owned();
        assert!(
            project(&input).is_some(),
            "the direct projection must return before testing its worker"
        );
        let projected = call(Operation::Project, input).expect("the real answer worker must return a transition");
        assert!(projected.contains(r#""allowed":true"#));
        let shown = call(Operation::Display, "A (Recommended)".to_owned()).expect("display must return");
        assert_eq!(shown, r#"{"text":"A","recommended":true}"#);
        assert!(call(Operation::Project, "not JSON".to_owned()).is_none());
    }
}
