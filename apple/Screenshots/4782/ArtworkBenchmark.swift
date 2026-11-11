import AppKit
import Foundation
import ImageIO

@main struct ArtworkBenchmark {
    @MainActor static func main() async throws {
        let context = CGContext(data: nil, width: 2048, height: 2048, bitsPerComponent: 8, bytesPerRow: 8192,
            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(NSColor.systemBlue.cgColor)
        context.fill(CGRect(x: 0, y: 0, width: 2048, height: 2048))
        let data = NSMutableData()
        let destination = CGImageDestinationCreateWithData(data, "public.png" as CFString, 1, nil)!
        CGImageDestinationAddImage(destination, context.makeImage()!, nil)
        CGImageDestinationFinalize(destination)
        let png = data as Data
        let output = CGContext(data: nil, width: 96, height: 96, bitsPerComponent: 8, bytesPerRow: 384,
            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        let url = URL(string: "https://images.example.com/logo.png")!
        let clock = ContinuousClock()
        func milliseconds(_ elapsed: Duration) -> Double {
            Double(elapsed.components.seconds) * 1000 + Double(elapsed.components.attoseconds) / 1e15
        }
        var results: [[String: Any]] = []
        for run in 1...3 {
            let before = clock.now
            for _ in 0..<100 {
                autoreleasepool {
                    let image = NSImage(data: png)!
                    let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil)!
                    output.draw(cg, in: CGRect(x: 0, y: 0, width: 96, height: 96))
                }
            }
            let baseline = milliseconds(before.duration(to: clock.now))
            let cache = ExtensionImageCache(deps: .init(fetch: { url in
                (png, HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: nil)!)
            }))
            let coldStart = clock.now
            let cg = await cache.image(url, maxPixel: 96)!
            let cold = milliseconds(coldStart.duration(to: clock.now))
            let warmStart = clock.now
            for _ in 0..<100 {
                let image = await cache.image(url, maxPixel: 96)!
                output.draw(image, in: CGRect(x: 0, y: 0, width: 96, height: 96))
            }
            let warm = milliseconds(warmStart.duration(to: clock.now))
            results.append(["run":run,"baseline_100_draws_ms":baseline,"cached_100_draws_ms":warm,
                            "cold_async_load_ms":cold,"decoded_width":cg.width,"decoded_height":cg.height,
                            "decoded_bytes":cg.bytesPerRow * cg.height])
        }
        let report: [String: Any] = ["compiler":"Swift -O","platform":"macOS",
            "scenario":"2048px PNG, 96px rendering, 100 repeated draws; isolated microbenchmark, not app scrolling",
            "results":results]
        let json = try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
        print(String(decoding:json,as:UTF8.self))
    }
}
