import Foundation

/// What the current user is entitled to.
enum Entitlement: Sendable {
    /// Everything, bought outright or by subscription.
    case pro
    /// The free tier: ads, and a cap on saved items.
    case free
}
