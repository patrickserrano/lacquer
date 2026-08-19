import Foundation

/// Formats a count for display.
public enum Formatter {
    /// Returns `count` rendered for the current locale.
    public static func short(_ count: Int) -> String {
        count.formatted(.number)
    }
}
