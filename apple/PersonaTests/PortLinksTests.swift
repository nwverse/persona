import Foundation
import Testing
@testable import Persona

/// Links to a port of the conversation's machine (docs/operations/ports.md「Recognizing links in replies」): localhost and its spellings, the machine's
/// own name, with paths; anything else on the web is not one.
struct PortLinksTests {
    private func one(_ s: String, hostnames: [String] = []) -> PortLink? {
        PortLinks.link(URL(string: s)!, hostnames: hostnames)
    }

    @Test func localHostsWithAPort() {
        #expect(one("http://localhost:5173/") == PortLink(port: 5173, path: "/", secure: false))
        #expect(one("http://127.0.0.1:8000/docs?x=1#top") == PortLink(port: 8000, path: "/docs?x=1#top", secure: false))
        #expect(one("http://0.0.0.0:3000") == PortLink(port: 3000, path: "/", secure: false))
        #expect(one("http://[::1]:4173/app") == PortLink(port: 4173, path: "/app", secure: false))
        #expect(one("https://localhost:8443/") == PortLink(port: 8443, path: "/", secure: true))
        #expect(one("http://app.localhost:5173/") == PortLink(port: 5173, path: "/", secure: false))
    }

    @Test func theMachinesOwnName() {
        #expect(one("http://MacBook.local:3000/pricing", hostnames: ["MacBook"]) == PortLink(port: 3000, path: "/pricing", secure: false))
        #expect(one("http://server:8080/", hostnames: ["server.local"]) == PortLink(port: 8080, path: "/", secure: false))
        #expect(one("http://server:8080/") == nil, "another machine's name")
    }

    @Test func notPortLinks() {
        #expect(one("https://example.com:8443/") == nil)
        #expect(one("http://192.0.2.10:23140/") == nil, "a LAN address is not this machine's loopback")
        #expect(one("http://localhost/") == nil, "no port")
        #expect(one("ftp://localhost:21/") == nil)
    }

    @Test func foundInText() {
        let text = """
        开发服务器起好了：http://localhost:5173/ ，改动会热更新。
          ➜  Local:   http://localhost:5173/
          ➜  Network: http://192.0.2.7:5173/
        API 在 127.0.0.1:8000/docs，旧的 `localhost:3000` 停了。别的：https://example.com/a
        """
        let found = PortLinks.find(in: text)
        #expect(found == [PortLink(port: 5173, path: "/", secure: false),
                          PortLink(port: 8000, path: "/docs", secure: false),
                          PortLink(port: 3000, path: "/", secure: false)])
        #expect(PortLinks.find(in: "没有链接。").isEmpty)
        #expect(PortLinks.find(in: "see http://example.com:3000/ and mylocalhost:3000").isEmpty)
    }
}
