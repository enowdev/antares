import Foundation

public struct APIError: Error, LocalizedError, Sendable {
    public var status: Int
    public var message: String
    public var isDashboardPasswordRequired: Bool { status == 428 }
    public var isUnauthorized: Bool { status == 401 }
    public var errorDescription: String? { message }

    public init(status: Int, body: Data) {
        self.status = status
        if let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
           let m = obj["message"] as? String {
            self.message = m
        } else if let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
           let e = obj["error"] as? String {
            self.message = e
        } else {
            self.message = HTTPURLResponse.localizedString(forStatusCode: status)
        }
    }
}

public final class APIClient: @unchecked Sendable {
    public let baseURL: URL
    public var bearerToken: String?
    public let session: URLSession

    public init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    public func url(_ path: String) -> URL {
        URL(string: path, relativeTo: baseURL)!
    }

    public func get<T: Decodable>(_ path: String) async throws -> T {
        try await send(path, method: "GET", body: nil as Data?)
    }

    public func post<T: Decodable, B: Encodable & Sendable>(_ path: String, body: B) async throws -> T {
        try await send(path, method: "POST", body: JSONEncoder().encode(body))
    }

    private func send<T: Decodable>(_ path: String, method: String, body: Data?) async throws -> T {
        var req = URLRequest(url: url(path))
        req.httpMethod = method
        req.httpBody = body
        if body != nil { req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        if let bearerToken, !bearerToken.isEmpty {
            req.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }
        let (data, resp) = try await session.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw APIError(status: status, body: data) }
        if T.self == EmptyResponse.self { return EmptyResponse() as! T }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

public struct EmptyResponse: Decodable, Sendable {
    public init() {}
}
