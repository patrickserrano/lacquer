import Foundation

/// Parses the wire format the apps exchange.
public enum Parser {
    /// Returns the fields of `line`, or nil when it is malformed.
    public static func fields(of line: String) -> [String]? {
        let parts = line.split(separator: "\t", omittingEmptySubsequences: false)
        return parts.count >= 2 ? parts.map(String.init) : nil
    }
}
