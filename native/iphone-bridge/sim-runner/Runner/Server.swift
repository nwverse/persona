// The bridge's way into the simulator (docs/extensions/iphone-bridge.md "Simulator backend"): one test that
// listens on 127.0.0.1 at the port the bridge chose, takes one connection that first sends the bridge's token,
// answers its requests as JSON lines, and ends when that connection closes, on `quit`, or when nobody
// connects in time. Every UI call runs on the main thread, where the test keeps the run loop going.

import Foundation
import Network
import XCTest

final class Serve: XCTestCase {
    /// An interaction that failed is the operation's error, not the test's: the runner keeps serving.
    override func record(_ issue: XCTIssue) {
        Ops.issue = issue.compactDescription
    }

    func testServe() throws {
        let env = ProcessInfo.processInfo.environment
        guard let token = env["PERSONA_RUNNER_TOKEN"], !token.isEmpty,
              let raw = env["PERSONA_RUNNER_PORT"], let number = UInt16(raw), let port = NWEndpoint.Port(rawValue: number)
        else {
            XCTFail("PERSONA_RUNNER_TOKEN and PERSONA_RUNNER_PORT are required")
            return
        }
        let server = Server(token: token, port: port)
        try server.start()
        while !server.done {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
        }
        server.stop()
    }
}

final class Server {
    private let token: String
    private let port: NWEndpoint.Port
    private let queue = DispatchQueue(label: "persona.sim-runner")
    private var listener: NWListener?
    private var connection: NWConnection?
    private var authenticated = false
    private var buffer = Data()
    /// Read on the main thread by the test's loop.
    private(set) var done = false
    /// Nobody connected with the token this long after start: the bridge went away, so stop.
    private let connectWithin: TimeInterval = 120

    init(token: String, port: NWEndpoint.Port) {
        self.token = token
        self.port = port
    }

    func start() throws {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: port)
        params.allowLocalEndpointReuse = true
        let listener = try NWListener(using: params)
        listener.newConnectionHandler = { [weak self] c in self?.accept(c) }
        listener.stateUpdateHandler = { [weak self] state in
            if case .failed = state { self?.finish() }
        }
        listener.start(queue: queue)
        self.listener = listener
        queue.asyncAfter(deadline: .now() + connectWithin) { [weak self] in
            guard let self, !self.authenticated else { return }
            self.finish()
        }
    }

    func stop() {
        connection?.cancel()
        listener?.cancel()
    }

    private func finish() {
        DispatchQueue.main.async { self.done = true }
    }

    private func accept(_ c: NWConnection) {
        // One client at a time: the bridge's.
        guard connection == nil else {
            c.cancel()
            return
        }
        connection = c
        c.stateUpdateHandler = { [weak self] state in
            switch state {
            case .failed, .cancelled:
                self?.closed(c)
            default:
                break
            }
        }
        c.start(queue: queue)
        receive(c)
    }

    private func closed(_ c: NWConnection) {
        guard connection === c else { return }
        connection = nil
        if authenticated {
            // The bridge is gone: nothing should keep running after it.
            finish()
        }
    }

    private func receive(_ c: NWConnection) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let data { self.buffer.append(data) }
            while let nl = self.buffer.firstIndex(of: 0x0A) {
                let line = self.buffer[self.buffer.startIndex..<nl]
                self.buffer.removeSubrange(self.buffer.startIndex...nl)
                self.handle(Data(line), on: c)
            }
            if self.buffer.count > 1 << 20 || isComplete || error != nil {
                c.cancel()
                return
            }
            self.receive(c)
        }
    }

    private func handle(_ line: Data, on c: NWConnection) {
        guard let req = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else {
            c.cancel()
            return
        }
        if !authenticated {
            // The first line proves it is the bridge that started this runner.
            if (req["token"] as? String) == token {
                authenticated = true
                send(["ok": true], on: c)
            } else {
                c.cancel()
            }
            return
        }
        let id = req["id"] as? String ?? ""
        if req["op"] as? String == "quit" {
            send(["id": id, "ok": true], on: c)
            finish()
            return
        }
        DispatchQueue.main.async {
            var reply = Ops.run(req)
            reply["id"] = id
            self.queue.async { self.send(reply, on: c) }
        }
    }

    private func send(_ obj: [String: Any], on c: NWConnection) {
        guard var data = try? JSONSerialization.data(withJSONObject: obj) else { return }
        data.append(0x0A)
        c.send(content: data, completion: .contentProcessed { _ in })
    }
}
