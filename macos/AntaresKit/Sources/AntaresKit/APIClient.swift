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
        try await send(url: url(path), method: "GET", body: nil)
    }

    public func get<T: Decodable>(_ path: String, query: [URLQueryItem]) async throws -> T {
        try await send(url: url(path, query: query), method: "GET", body: nil)
    }

    public func getData(_ path: String, query: [URLQueryItem] = []) async throws -> Data {
        try await raw(url: url(path, query: query), method: "GET", body: nil)
    }

    public func post<T: Decodable, B: Encodable & Sendable>(_ path: String, body: B) async throws -> T {
        try await send(url: url(path), method: "POST", body: JSONEncoder().encode(body))
    }

    public func put<T: Decodable, B: Encodable & Sendable>(_ path: String, body: B) async throws -> T {
        try await send(url: url(path), method: "PUT", body: JSONEncoder().encode(body))
    }

    public func delete<T: Decodable>(_ path: String) async throws -> T {
        try await send(url: url(path), method: "DELETE", body: nil)
    }

    public func delete<T: Decodable>(_ path: String, query: [URLQueryItem]) async throws -> T {
        try await send(url: url(path, query: query), method: "DELETE", body: nil)
    }

    private func url(_ path: String, query: [URLQueryItem]) -> URL {
        var components = URLComponents(url: url(path), resolvingAgainstBaseURL: true)!
        if !query.isEmpty {
            components.queryItems = query
        }
        return components.url!
    }

    private func send<T: Decodable>(url: URL, method: String, body: Data?) async throws -> T {
        let data = try await raw(url: url, method: method, body: body)
        if T.self == EmptyResponse.self { return EmptyResponse() as! T }
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func raw(url: URL, method: String, body: Data?) async throws -> Data {
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.httpBody = body
        if body != nil { req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        if let bearerToken, !bearerToken.isEmpty {
            req.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }
        let (data, resp) = try await session.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw APIError(status: status, body: data) }
        return data
    }
}

public struct EmptyResponse: Decodable, Sendable {
    public init() {}
}
