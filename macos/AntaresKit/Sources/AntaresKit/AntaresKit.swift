import Foundation

public enum AntaresKit {
    public static let defaultBaseURL = URL(string: "http://127.0.0.1:8787")!

    /// Shared by `APIClient` and `RuntimeSupervisor`. `ANTARES_URL` overrides the loopback default.
    public static func resolveBaseURL(
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> URL {
        environment["ANTARES_URL"].flatMap(URL.init(string:)) ?? defaultBaseURL
    }
}
