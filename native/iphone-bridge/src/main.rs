//! persona-iphone-bridge: operates a paired iPhone over RemotePairing for a Persona node.
//! Wire contract: docs/extensions/iphone-bridge.md.

mod device;
mod error;
mod files;
mod geometry;
mod keys;
mod ops;
mod pair;
mod record;
mod run;
mod shot;
mod wire;

use std::path::{Path, PathBuf};
use std::time::Duration;

use serde_json::Value;
use tokio::io::{AsyncWriteExt, BufReader};

use crate::device::{Device, Target};
use crate::error::{BridgeError, Code};
use crate::ops::Action;
use crate::run::{Outcome, Phone};

const USAGE: &str = "usage: persona-iphone-bridge version | call | serve --udid <udid> --record <path> --host <address> [--port <port>] [--timeout-ms <ms>] | usb-list --json | pair --udid <udid> --out <path> | match --record <path> --identifier <id> --auth-tag <tag>";

fn version_line() -> String {
    format!(
        "persona-iphone-bridge {} (wire {})",
        env!("CARGO_PKG_VERSION"),
        wire::WIRE_VERSION
    )
}

/// `--name value` flags (and bare `--switch`es) after the subcommand.
struct Flags(Vec<(String, String)>);

impl Flags {
    fn parse(args: &[String], allowed: &[&str], switches: &[&str]) -> Result<Self, BridgeError> {
        let mut out = Vec::new();
        let mut it = args.iter();
        while let Some(a) = it.next() {
            let name = a
                .strip_prefix("--")
                .filter(|n| allowed.contains(n) || switches.contains(n))
                .ok_or_else(|| BridgeError::bad_request(format!("unexpected argument {a:?}; {USAGE}")))?;
            if switches.contains(&name) {
                out.push((name.to_owned(), String::new()));
                continue;
            }
            let v = it
                .next()
                .ok_or_else(|| BridgeError::bad_request(format!("--{name} needs a value")))?;
            out.push((name.to_owned(), v.clone()));
        }
        Ok(Self(out))
    }

    fn get(&self, name: &str) -> Option<&str> {
        self.0.iter().rev().find(|(n, _)| n == name).map(|(_, v)| v.as_str())
    }

    fn req(&self, name: &str) -> Result<&str, BridgeError> {
        self.get(name)
            .filter(|v| !v.is_empty())
            .ok_or_else(|| BridgeError::bad_request(format!("--{name} is required; {USAGE}")))
    }

    fn num<T: std::str::FromStr>(&self, name: &str) -> Result<Option<T>, BridgeError> {
        self.get(name)
            .map(|v| {
                v.parse()
                    .map_err(|_| BridgeError::bad_request(format!("--{name}: {v:?} is not a number")))
            })
            .transpose()
    }
}

fn failed(e: &BridgeError) -> Outcome {
    Outcome {
        code: 1,
        stderr_error: Some(wire::error_line(None, None, e)),
    }
}

async fn print_line(line: &str) -> Outcome {
    let mut out = tokio::io::stdout();
    let written = async {
        out.write_all(format!("{line}\n").as_bytes()).await?;
        out.flush().await
    };
    match written.await {
        Ok(()) => Outcome {
            code: 0,
            stderr_error: None,
        },
        Err(e) => failed(&BridgeError::internal(format!("write stdout: {e}"))),
    }
}

/// A one-shot command whose answer is a bare JSON value.
async fn one_shot(result: Result<Value, BridgeError>) -> Outcome {
    match result {
        Ok(v) => print_line(&v.to_string()).await,
        Err(e) => failed(&e),
    }
}

async fn serve(args: &[String]) -> Outcome {
    let setup = || -> Result<_, BridgeError> {
        let flags = Flags::parse(args, &["udid", "record", "host", "port", "timeout-ms"], &[])?;
        let target = Target {
            udid: flags.req("udid")?.to_owned(),
            host: flags.req("host")?.to_owned(),
            port: flags.num("port")?.unwrap_or(wire::DEFAULT_PORT),
        };
        let record = PathBuf::from(flags.req("record")?);
        let timeout = wire::clamp_timeout(flags.num("timeout-ms")?);
        Ok((target, record, timeout))
    };
    let (target, record, timeout) = match setup() {
        Ok(s) => s,
        Err(e) => return failed(&e),
    };
    let connected = tokio::time::timeout(Duration::from_millis(timeout), async {
        let mut device = Device::connect(&target, &record).await?;
        let info = device.run(Action::DeviceInfo).await?;
        Ok::<_, BridgeError>((device, info))
    })
    .await
    .unwrap_or_else(|_| {
        Err(BridgeError::new(
            Code::Timeout,
            format!("not connected within {timeout} ms"),
        ))
    });
    let (mut device, info) = match connected {
        Ok(c) => c,
        Err(e) => return failed(&e),
    };
    let mut input = BufReader::new(tokio::io::stdin());
    let mut out = tokio::io::stdout();
    run::serve(&mut device, info, &mut input, &mut out).await
}

async fn call() -> Outcome {
    let mut input = BufReader::new(tokio::io::stdin());
    let mut out = tokio::io::stdout();
    run::call(&mut input, &mut out, async |spec: wire::DeviceSpec| {
        let target = Target {
            port: spec.port(),
            udid: spec.udid,
            host: spec.address,
        };
        Device::connect(&target, Path::new(&spec.pairing_file)).await
    })
    .await
}

async fn dispatch(args: &[String]) -> Outcome {
    let (cmd, rest) = match args.split_first() {
        Some((c, r)) => (c.as_str(), r),
        None => ("", &[][..]),
    };
    match cmd {
        "version" | "--version" | "-V" if rest.is_empty() => print_line(&version_line()).await,
        "call" if rest.is_empty() => call().await,
        "serve" => serve(rest).await,
        "usb-list" => match Flags::parse(rest, &[], &["json"]) {
            Ok(_) => one_shot(pair::usb_list().await).await,
            Err(e) => failed(&e),
        },
        "pair" => {
            let result = async {
                let flags = Flags::parse(rest, &["udid", "out"], &[])?;
                pair::pair(flags.req("udid")?, Path::new(flags.req("out")?)).await
            };
            one_shot(result.await).await
        }
        "match" => {
            let result = async {
                let flags = Flags::parse(rest, &["record", "identifier", "auth-tag"], &[])?;
                pair::match_record(
                    Path::new(flags.req("record")?),
                    flags.req("identifier")?,
                    flags.req("auth-tag")?,
                )
                .await
            };
            one_shot(result.await).await
        }
        _ => failed(&BridgeError::bad_request(USAGE)),
    }
}

#[tokio::main(flavor = "current_thread")]
async fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let outcome = dispatch(&args).await;
    if let Some(line) = outcome.stderr_error {
        eprintln!("{line}");
    }
    std::process::exit(outcome.code);
}

#[cfg(test)]
mod tests {
    use super::*;

    fn strings(a: &[&str]) -> Vec<String> {
        a.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn reads_flags() {
        let f = Flags::parse(&strings(&["--udid", "U", "--port", "62078"]), &["udid", "port"], &[]).unwrap();
        assert_eq!(f.req("udid").unwrap(), "U");
        assert_eq!(f.num::<u16>("port").unwrap(), Some(62078));
        assert!(f.req("host").is_err());
        assert!(Flags::parse(&strings(&["--udid"]), &["udid"], &[]).is_err());
        assert!(Flags::parse(&strings(&["--rm", "x"]), &["udid"], &[]).is_err());
        assert!(Flags::parse(&strings(&["--json"]), &[], &["json"]).is_ok());
        let f = Flags::parse(&strings(&["--port", "many"]), &["port"], &[]).unwrap();
        assert!(f.num::<u16>("port").is_err());
    }

    #[test]
    fn says_its_version() {
        assert_eq!(version_line(), "persona-iphone-bridge 0.1.0 (wire 1)");
    }

    #[tokio::test]
    async fn bad_command_lines_fail_with_usage() {
        let o = dispatch(&strings(&["format"])).await;
        assert_eq!(o.code, 1);
        assert!(o.stderr_error.unwrap().contains("bad_request"));
        let o = dispatch(&strings(&["serve", "--udid", "U"])).await;
        assert!(o.stderr_error.unwrap().contains("--host is required"));
        let o = dispatch(&strings(&["pair", "--udid", "U"])).await;
        assert!(o.stderr_error.unwrap().contains("--out is required"));
        let o = dispatch(&strings(&["match", "--record", "/r", "--identifier", "i"])).await;
        assert!(o.stderr_error.unwrap().contains("--auth-tag is required"));
    }
}
