//! DEBUG-only answer projections with a bounded native wait.
//!
//! One detached Rust worker owns at most the job it is currently evaluating. The JNI caller waits at most one second.
//! At most one additional job can wait in the channel; no job, result or error is logged and Java is never called back.

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

/// A full queue or a slow native projection is a diagnostic failure, not a reason to retain unbounded input.
pub(crate) fn call(operation: Operation, input: String) -> Option<String> {
    let (sender, receiver) = sync_channel(1);
    let job = Job {
        operation,
        input,
        result: sender,
    };
    // This call is already on the client's background shadow worker. Waiting for one slot retains at most one job;
    // the main thread never enters this function. The result wait below is bounded independently.
    worker().lock().ok()?.send(job).ok()?;
    receiver.recv_timeout(Duration::from_secs(1)).ok().flatten()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;

    #[test]
    fn real_worker_projects_and_displays_without_retaining_an_input_queue() {
        let input = r#"{"interaction":{"kind":"question","questions":[{"question":"One","multi_select":false,"options":[{"label":"A"},{"label":"B"}]},{"question":"Two","multi_select":false,"options":[{"label":"C"},{"label":"D"}]}]},"operation":{"action":"pick_option","page":0,"picks":{},"other":{"One":"custom"},"sending":false,"option_index":1}}"#.to_owned();
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

    #[test]
    fn a_full_worker_rejects_new_input_instead_of_queuing_beyond_one() {
        let (sender, receiver) = mpsc::sync_channel::<Job>(1);
        let (started_sender, started_receiver) = mpsc::sync_channel(0);
        let (release_sender, release_receiver) = mpsc::sync_channel(0);
        let worker = std::thread::spawn(move || {
            let job = receiver.recv().expect("the first job must arrive");
            started_sender.send(()).expect("the test must observe the busy job");
            release_receiver.recv().expect("the test must release the busy job");
            let _ = job.result.send(Some("done".to_owned()));
        });
        let (result_sender, result_receiver) = mpsc::sync_channel(1);
        sender
            .send(Job {
                operation: Operation::Display,
                input: "first".to_owned(),
                result: result_sender,
            })
            .expect("the test must start its controlled worker");
        started_receiver
            .recv_timeout(Duration::from_secs(1))
            .expect("the worker must be busy");
        let (other_sender, other_receiver) = mpsc::sync_channel(1);
        sender
            .try_send(Job {
                operation: Operation::Display,
                input: "second".to_owned(),
                result: other_sender,
            })
            .expect("at most one additional input may wait");
        let (third_sender, _) = mpsc::sync_channel(1);
        assert!(
            sender
                .try_send(Job {
                    operation: Operation::Display,
                    input: "third".to_owned(),
                    result: third_sender
                })
                .is_err(),
            "the queue must reject another input once its one slot is full"
        );
        release_sender.send(()).expect("the controlled worker must finish");
        assert_eq!(
            result_receiver
                .recv_timeout(Duration::from_secs(1))
                .ok()
                .flatten()
                .as_deref(),
            Some("done")
        );
        worker.join().expect("the controlled worker must finish without a panic");
    }
}
