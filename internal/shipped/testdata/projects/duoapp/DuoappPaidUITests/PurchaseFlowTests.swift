import XCTest

/// The paid product declares a UI bundle; the free one deliberately does not,
/// which is why `ui_test_target` is per-product and blank has to stay a
/// first-class value.
final class PurchaseFlowTests: XCTestCase {
    func testPaywallAppears() throws {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.staticTexts["Duoapp"].waitForExistence(timeout: 5))
    }
}
