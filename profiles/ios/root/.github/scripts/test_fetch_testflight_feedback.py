#!/usr/bin/env python3
"""
Tests for fetch_testflight_feedback.py.

    python3 .github/scripts/test_fetch_testflight_feedback.py

stdlib `unittest` only, and no network: the fixtures below are real App Store
Connect responses, captured from
`GET /v1/apps/{id}/betaFeedbackScreenshotSubmissions` with this script's own
`fields[]`/`include` parameters, with the tester email replaced. Every attribute
name in them is therefore the API's, not a guess — which is the failure these
tests exist to catch (see the module docstring of the script under test).

The pre-commit hook runs this on any change under .github/scripts/, so a rename
that breaks the parse cannot reach a commit, let alone the nightly run.
"""
import contextlib
import io
import json
import os
import sys
import unittest
import urllib.error

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fetch_testflight_feedback as tf  # noqa: E402

# ── Fixtures: verbatim App Store Connect payloads ─────────────────────────────

SHOT_URL = (
    "https://tf-feedback.itunes.apple.com/eimg/Bbk/JXg/JYY/FZE/G8g/Q_elMz_9GJE/"
    "original.jpg?i_for=1234567890&AWSAccessKeyId=MKIA9C0TVRX1ZL0VZ1YK"
    "&Expires=1789516800&Signature=OSug7fV5Y%2BkqfeUdoFk%2FW3qzn78%3D"
    "&p_sig=B1qFx0GABtIUFqhKFaNFNLBxTsI"
)
BUILD_ID = "97914e17-3f07-4072-ba32-053f78b4021f"
PRV_LINK = f"https://api.appstoreconnect.apple.com/v1/builds/{BUILD_ID}/preReleaseVersion"


def screenshot_row(**overrides):
    """One `betaFeedbackScreenshotSubmissions` row as the API returns it."""
    row = {
        "type": "betaFeedbackScreenshotSubmissions",
        "id": "ACQDK-ZZ9WhIGV6JA_C6HMY",
        "attributes": {
            "comment": "Wtf is up with this button",
            "createdDate": "2026-06-07T23:35:18.01Z",
            "deviceModel": "iPhone18_2",
            "email": "tester@example.com",
            "osVersion": "26.6",
            "screenshots": [{
                "expirationDate": "2026-09-16T00:00:00Z",
                "height": 2868,
                "url": SHOT_URL,
                "width": 1320,
            }],
        },
        "relationships": {
            "build": {"data": {"type": "builds", "id": BUILD_ID}},
            "tester": {"data": {"type": "betaTesters",
                                "id": "aeb88e6b-5df7-4b0e-8776-c83c72c15127"}},
        },
    }
    row["attributes"].update(overrides)
    return row


def included_build(with_prv_link=True):
    """The `builds` entry from `included`, keyed the way _paginate keys it."""
    build = {
        "type": "builds",
        "id": BUILD_ID,
        "attributes": {"version": "3"},
        "relationships": {},
    }
    if with_prv_link:
        build["relationships"]["preReleaseVersion"] = {
            "links": {
                "self": f"https://api.appstoreconnect.apple.com/v1/builds/{BUILD_ID}"
                        f"/relationships/preReleaseVersion",
                "related": PRV_LINK,
            }
        }
    return {("builds", BUILD_ID): build}


PRERELEASE_RESPONSE = {
    "data": {
        "type": "preReleaseVersions",
        "id": "c6ee72ef-c93e-43aa-aff0-0af1bc74083b",
        "attributes": {"version": "1.3", "platform": "IOS"},
    }
}


class FakeGet:
    """Stands in for tf._get, recording every URL it is asked for."""

    def __init__(self, responses):
        self.responses = responses
        self.urls = []

    def __call__(self, url, token):
        self.urls.append(url)
        value = self.responses[url]
        if isinstance(value, Exception):
            raise value
        return value


class Patched:
    """Swap module attributes for the duration of a block."""

    def __init__(self, **attrs):
        self.attrs = attrs
        self.saved = {}

    def __enter__(self):
        for name, value in self.attrs.items():
            self.saved[name] = getattr(tf, name)
            setattr(tf, name, value)
        return self

    def __exit__(self, *exc):
        for name, value in self.saved.items():
            setattr(tf, name, value)


# ── The regression itself: no silently blank fields ───────────────────────────

class RequestedFieldsTests(unittest.TestCase):
    """Every attribute read must be requested, so ASC validates its name."""

    def test_every_required_attribute_is_requested(self):
        # Anything in REQUIRED_ATTRS but not FIELDS would never be returned,
        # and would fail as "absent" on every single submission.
        for kind, required in tf.REQUIRED_ATTRS.items():
            for key in required:
                self.assertIn(key, tf.FIELDS[kind],
                              f"{kind}: {key} is required but never requested")

    def test_every_optional_attribute_is_requested(self):
        for kind, optional in tf.OPTIONAL_ATTRS.items():
            for key in optional:
                self.assertIn(key, tf.FIELDS[kind],
                              f"{kind}: {key} is optional but never requested")

    def test_the_fields_that_broke_477_are_not_requested(self):
        # `appVersion` and `timestamp` are not attributes of either resource.
        # Requesting either one now fails the whole run with HTTP 400, so this
        # guards against someone reintroducing the names the bug used.
        for kind in tf.FIELDS:
            self.assertNotIn("appVersion", tf.FIELDS[kind])
            self.assertNotIn("timestamp", tf.FIELDS[kind])

    def test_relationships_needed_for_the_version_are_requested(self):
        # Naming any subset of fields[] suppresses the whole relationships
        # object, so `build` must be listed or no row can be mapped to a build.
        for kind in tf.FIELDS:
            self.assertIn("build", tf.FIELDS[kind])


class AttributeStrictnessTests(unittest.TestCase):
    """A missing attribute raises; a null attribute does not."""

    def test_missing_required_attribute_raises(self):
        row = screenshot_row()
        del row["attributes"]["createdDate"]
        with self.assertRaises(tf.MissingAttribute) as caught:
            tf._attributes("screenshot", row)
        self.assertEqual(caught.exception.key, "createdDate")
        self.assertEqual(caught.exception.submission_id, "ACQDK-ZZ9WhIGV6JA_C6HMY")

    def test_missing_attribute_message_names_the_keys_present(self):
        row = screenshot_row()
        del row["attributes"]["osVersion"]
        with self.assertRaises(tf.MissingAttribute) as caught:
            tf._attributes("screenshot", row)
        message = str(caught.exception)
        self.assertIn("osVersion", message)
        self.assertIn("deviceModel", message, "the message should list what WAS present")

    def test_null_attribute_is_absence_not_breakage(self):
        # A screenshot filed with no comment is normal; it must not raise.
        attrs = tf._attributes("screenshot", screenshot_row(comment=None))
        self.assertIsNone(attrs["comment"])

    def test_missing_optional_attribute_does_not_raise(self):
        row = screenshot_row()
        del row["attributes"]["comment"]
        tf._attributes("screenshot", row)  # must not raise

    def test_all_required_keys_are_checked(self):
        # Each required key independently raises when removed — otherwise a
        # check could be silently absent from the loop.
        for key in tf.REQUIRED_ATTRS["screenshot"]:
            row = screenshot_row()
            del row["attributes"][key]
            with self.assertRaises(tf.MissingAttribute, msg=f"{key} was not checked"):
                tf._attributes("screenshot", row)


class AppVersionTests(unittest.TestCase):
    """The version comes from the build relationship, never from an attribute."""

    def test_marketing_version_and_build_number(self):
        fake = FakeGet({PRV_LINK: PRERELEASE_RESPONSE})
        with Patched(_get=fake):
            got = tf.app_version(screenshot_row(), included_build(), "tok", {})
        self.assertEqual(got, "1.3 (3)")
        self.assertEqual(fake.urls, [PRV_LINK])

    def test_the_prerelease_lookup_is_cached_per_build(self):
        fake = FakeGet({PRV_LINK: PRERELEASE_RESPONSE})
        cache, included = {}, included_build()
        with Patched(_get=fake):
            for _ in range(4):
                tf.app_version(screenshot_row(), included, "tok", cache)
        self.assertEqual(len(fake.urls), 1, "four submissions, one build, one request")

    def test_no_build_relationship_yields_none_so_the_field_is_omitted(self):
        row = screenshot_row()
        row["relationships"] = {}
        with Patched(_get=FakeGet({})):
            self.assertIsNone(tf.app_version(row, {}, "tok", {}))

    def test_build_number_alone_when_there_is_no_prerelease_version(self):
        with Patched(_get=FakeGet({})):
            got = tf.app_version(screenshot_row(), included_build(with_prv_link=False),
                                 "tok", {})
        self.assertEqual(got, "build 3")

    def test_prerelease_http_failure_degrades_to_the_build_number(self):
        fake = FakeGet({PRV_LINK: urllib.error.HTTPError(PRV_LINK, 429, "slow down", {}, None)})
        with Patched(_get=fake):
            got = tf.app_version(screenshot_row(), included_build(), "tok", {})
        self.assertEqual(got, "build 3")


class ScreenshotTests(unittest.TestCase):
    """The screenshot is the payload; it must survive parsing intact."""

    def test_screenshot_url_and_dimensions_are_read(self):
        got = tf.screenshots(screenshot_row()["attributes"])
        self.assertEqual(len(got), 1)
        self.assertEqual(got[0]["url"], SHOT_URL)
        self.assertEqual((got[0]["width"], got[0]["height"]), (1320, 2868))
        self.assertEqual(got[0]["expires"], "2026-09-16T00:00:00Z")

    def test_multiple_screenshots_are_all_kept(self):
        attrs = screenshot_row()["attributes"]
        attrs["screenshots"] = attrs["screenshots"] * 3
        self.assertEqual(len(tf.screenshots(attrs)), 3)

    def test_no_screenshots_is_an_empty_list_not_an_error(self):
        self.assertEqual(tf.screenshots(screenshot_row(screenshots=None)), [])
        self.assertEqual(tf.screenshots(screenshot_row(screenshots=[])), [])

    def test_unusable_urls_are_dropped(self):
        attrs = screenshot_row()["attributes"]
        attrs["screenshots"] = [
            {"url": "http://insecure.example.com/a.jpg"},
            {"url": "https://example.com/a.jpg?x=(1)"},
            {"url": None},
            "not a dict",
        ]
        self.assertEqual(tf.screenshots(attrs), [])

    def test_markdown_url_rejects_characters_that_break_out_of_an_embed(self):
        for bad in ("https://e.example.com/a.jpg?x=)", "https://e.example.com/a b.jpg",
                    "https://e.example.com/<x>.jpg", 'https://e.example.com/"x.jpg',
                    "ftp://e.example.com/a.jpg", "", None):
            self.assertEqual(tf.markdown_url(bad), "", f"should reject {bad!r}")
        self.assertEqual(tf.markdown_url(SHOT_URL), SHOT_URL)


CRASH_LOG_LINK = (
    "https://api.appstoreconnect.apple.com/v1/betaFeedbackCrashSubmissions/X/crashLog"
)


class CrashLogTests(unittest.TestCase):
    """crashLog is a relationship carrying inline logText — not a `url`."""

    def test_crash_log_is_followed_and_read_from_log_text(self):
        fake = FakeGet({CRASH_LOG_LINK: {"data": {"attributes": {"logText": "Thread 0 crashed"}}}})
        with Patched(_get=fake):
            self.assertEqual(tf.crash_log_text(CRASH_LOG_LINK, "X", "tok"), "Thread 0 crashed")

    def test_no_crash_log_link_makes_no_request_at_all(self):
        fake = FakeGet({})
        with Patched(_get=fake):
            self.assertEqual(tf.crash_log_text("", "X", "tok"), "")
        self.assertEqual(fake.urls, [])

    def test_crash_log_http_failure_does_not_lose_the_submission(self):
        fake = FakeGet({CRASH_LOG_LINK: urllib.error.HTTPError(
            CRASH_LOG_LINK, 500, "boom", {}, None)})
        with Patched(_get=fake):
            self.assertEqual(tf.crash_log_text(CRASH_LOG_LINK, "X", "tok"), "")

    def test_a_missing_logText_attribute_is_empty_not_a_crash(self):
        fake = FakeGet({CRASH_LOG_LINK: {"data": {"attributes": {}}}})
        with Patched(_get=fake):
            self.assertEqual(tf.crash_log_text(CRASH_LOG_LINK, "X", "tok"), "")

    def test_fetch_captures_the_link_without_downloading_the_log(self):
        # One request per crash log, and every crash ever submitted comes back
        # on every run — so fetch must NOT resolve them.
        row = {
            "type": "betaFeedbackCrashSubmissions", "id": "CRASH1",
            "attributes": {"createdDate": "2026-06-07T23:35:18.01Z",
                           "deviceModel": "iPhone18_2", "osVersion": "26.6",
                           "email": "tester@example.com", "comment": "boom"},
            "relationships": {
                "build": {"data": {"type": "builds", "id": BUILD_ID}},
                "crashLog": {"links": {"related": CRASH_LOG_LINK}},
            },
        }
        fake = FakeGet({PRV_LINK: PRERELEASE_RESPONSE})
        with Patched(_paginate=lambda *a, **k: ([row], included_build()), _get=fake):
            rows, failures = tf.fetch("crash", "123", "tok")
        self.assertEqual(failures, [])
        self.assertEqual(rows[0]["crashLogLink"], CRASH_LOG_LINK)
        self.assertEqual(rows[0]["crashLog"], "", "fetch must not download the log")
        self.assertNotIn(CRASH_LOG_LINK, fake.urls)


# ── Rendering: an unknown field is omitted, never blank ───────────────────────

def record(**overrides):
    base = {
        "kind": "screenshot", "id": "FEEDBACK1", "tester": "tester@example.com",
        "device": "iPhone18_2", "os": "26.6", "appVersion": "1.3 (3)",
        "createdDate": "2026-06-07T23:35:18.01Z", "comment": "Wtf is up with this button",
        "screenshots": [{"url": SHOT_URL, "width": 1320, "height": 2868,
                         "expires": "2026-09-16T00:00:00Z"}],
        "crashLogLink": "", "crashLog": "",
    }
    base.update(overrides)
    return base


class IssueBodyTests(unittest.TestCase):
    def test_the_body_carries_version_date_and_screenshot(self):
        body = tf.issue_body(record())
        self.assertIn("- **App version:** 1.3 (3)", body)
        self.assertIn("- **Submitted:** 2026-06-07T23:35:18.01Z", body)
        self.assertIn(f"![TestFlight screenshot 1]({SHOT_URL})", body)

    def test_never_renders_the_blank_fields_from_477(self):
        # The exact defect: `- **App version:** ` and `- **When:** ` with nothing
        # after them. An unknown value must remove the line, not empty it.
        body = tf.issue_body(record(appVersion=None, createdDate=""))
        self.assertNotIn("App version", body)
        self.assertNotIn("Submitted", body)
        for line in body.splitlines():
            # `- **X:**` is a field line; a bare `**X:**` is a section heading.
            if line.startswith("- **"):
                self.assertFalse(line.rstrip().endswith(":**"),
                                 f"blank field rendered: {line!r}")

    def test_screenshot_expiry_is_stated_so_a_dead_link_is_not_a_missing_one(self):
        body = tf.issue_body(record())
        self.assertIn("expires 2026-09-16T00:00:00Z", body)
        self.assertIn("betaFeedbackScreenshotSubmissions/FEEDBACK1", body,
                      "the body must say how to re-fetch an expired screenshot")

    def test_a_screenshot_submission_with_no_image_says_so_explicitly(self):
        body = tf.issue_body(record(screenshots=[]))
        self.assertIn("none returned by App Store Connect", body)

    def test_every_line_of_a_multiline_comment_stays_quoted(self):
        body = tf.issue_body(record(comment="line one\n- **App version:** injected"))
        for line in body.splitlines():
            if "injected" in line:
                self.assertTrue(line.startswith("> "), f"escaped the blockquote: {line!r}")

    def test_the_dedup_marker_is_present_and_exact(self):
        self.assertIn(tf.dedup_marker("FEEDBACK1"), tf.issue_body(record()))

    def test_a_long_crash_log_is_truncated_inside_the_issue_limit(self):
        # Sized past GitHub's 65536-character issue body limit, so a body that
        # merely *says* it truncated while carrying the whole log still fails.
        # Filled with a character that appears nowhere else in the body, so the
        # count below measures only the log.
        log = "§" * 200000
        body = tf.issue_body(record(kind="crash", screenshots=[], crashLog=log))
        self.assertIn("… truncated", body)
        self.assertLess(len(body), 65536, "the body would be rejected by GitHub")
        self.assertNotIn(log, body, "the full log is still in the body")
        self.assertEqual(body.count("§"), tf.CRASH_LOG_CHARS)

    def test_a_short_crash_log_is_not_marked_truncated(self):
        body = tf.issue_body(record(kind="crash", screenshots=[], crashLog="Thread 0"))
        self.assertIn("Thread 0", body)
        self.assertNotIn("… truncated", body)


class IssueTitleTests(unittest.TestCase):
    def test_title_carries_the_version(self):
        self.assertEqual(tf.issue_title(record()),
                         "[TestFlight screenshot] 1.3 (3) iPhone18_2 26.6 — tester@example.com")

    def test_unknown_version_leaves_no_gap(self):
        title = tf.issue_title(record(appVersion=None))
        self.assertNotIn("  ", title)
        self.assertEqual(title, "[TestFlight screenshot] iPhone18_2 26.6 — tester@example.com")


class SanitizeTests(unittest.TestCase):
    """Pre-existing trust boundary — retained because the body grew new fields."""

    def test_comment_delimiters_are_stripped_to_a_fixpoint(self):
        self.assertNotIn("<!--", tf.sanitize("<!<!------>"))
        self.assertNotIn("-->", tf.sanitize("<!<!------>"))

    def test_a_tester_cannot_forge_a_dedup_marker(self):
        forged = tf.dedup_marker("OTHER")
        self.assertNotIn(forged, tf.sanitize(f"please dedup me {forged}"))


# ── End to end over a whole faked page ────────────────────────────────────────

class FetchTests(unittest.TestCase):
    def _page(self, rows):
        return {"data": rows, "included": [list(included_build().values())[0]],
                "links": {}}

    def test_fetch_requests_the_documented_fields_and_include(self):
        fake = FakeGet({})
        captured = {}

        def paginate(url, token, params, limit=50):
            captured["url"], captured["params"] = url, params
            return [screenshot_row()], included_build()

        with Patched(_paginate=paginate, _get=FakeGet({PRV_LINK: PRERELEASE_RESPONSE})):
            rows, failures = tf.fetch("screenshot", "123", "tok")
        self.assertEqual(failures, [])
        self.assertEqual(captured["params"]["include"], "build")
        self.assertEqual(
            captured["params"]["fields[betaFeedbackScreenshotSubmissions]"],
            ",".join(tf.FIELDS["screenshot"]))
        self.assertIn("betaFeedbackScreenshotSubmissions", captured["url"])
        self.assertEqual(rows[0]["appVersion"], "1.3 (3)")
        self.assertEqual(rows[0]["createdDate"], "2026-06-07T23:35:18.01Z")
        self.assertEqual(len(rows[0]["screenshots"]), 1)

    def test_one_unreadable_row_does_not_drop_the_readable_ones(self):
        good = screenshot_row()
        bad = screenshot_row()
        bad["id"] = "BROKEN"
        del bad["attributes"]["createdDate"]

        def paginate(url, token, params, limit=50):
            return [bad, good], included_build()

        with Patched(_paginate=paginate, _get=FakeGet({PRV_LINK: PRERELEASE_RESPONSE})):
            rows, failures = tf.fetch("screenshot", "123", "tok")
        self.assertEqual(len(rows), 1)
        self.assertEqual(len(failures), 1)
        self.assertEqual(failures[0].submission_id, "BROKEN")

    def test_paginate_sends_fields_unencoded_enough_to_read(self):
        seen = {}

        def get(url, token):
            seen["url"] = url
            return {"data": [], "links": {}}

        with Patched(_get=get):
            tf._paginate("https://api.example.com/v1/x", "tok",
                         {"fields[builds]": "version,preReleaseVersion"})
        self.assertIn("fields[builds]=version,preReleaseVersion", seen["url"])
        self.assertIn("limit=50", seen["url"])

    def test_paginate_merges_included_across_pages(self):
        pages = [
            {"data": [{"id": "a"}], "included": [{"type": "builds", "id": "B1"}],
             "links": {"next": "https://api.example.com/page2"}},
            {"data": [{"id": "b"}], "included": [{"type": "builds", "id": "B2"}],
             "links": {}},
        ]
        with Patched(_get=lambda url, token: pages.pop(0)):
            rows, included = tf._paginate("https://api.example.com/v1/x", "tok", {})
        self.assertEqual([r["id"] for r in rows], ["a", "b"])
        self.assertEqual(sorted(k[1] for k in included), ["B1", "B2"])


class MainExitCodeTests(unittest.TestCase):
    """A submission the script could not read must turn the run red.

    This is the whole point of #477: a field the API does not return has to be
    loud. An unreadable row that still exits 0 is a nightly job reporting
    success over data it silently dropped.
    """

    ENV = {
        "APP_STORE_CONNECT_ISSUER_ID": "issuer",
        "APP_STORE_CONNECT_KEY_IDENTIFIER": "keyid",
        "APP_STORE_CONNECT_PRIVATE_KEY": "pem",
        "APP_ID": "123",
    }

    def _run(self, per_kind, exists=lambda fid: False, crash_log=None):
        """Run main() with the API and `gh` stubbed out. Returns (code, filed)."""
        filed = []
        saved = {k: os.environ.get(k) for k in self.ENV}
        os.environ.update(self.ENV)
        patches = dict(
            make_jwt=lambda *a: "token",
            fetch=lambda kind, app, tok: per_kind[kind],
            issue_exists=exists,
            create_issue=filed.append,
        )
        if crash_log is not None:
            patches["crash_log_text"] = crash_log
        try:
            with Patched(**patches), contextlib.redirect_stdout(io.StringIO()), \
                    contextlib.redirect_stderr(io.StringIO()):
                return tf.main(), filed
        finally:
            for k, v in saved.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v

    def test_a_clean_run_exits_zero_and_files_the_issues(self):
        code, filed = self._run({"screenshot": ([record()], []), "crash": ([], [])})
        self.assertEqual(code, 0)
        self.assertEqual(len(filed), 1)

    def test_an_unreadable_submission_exits_non_zero(self):
        broken = tf.MissingAttribute("betaFeedbackScreenshotSubmissions", "BAD",
                                     "createdDate", {"comment"})
        code, filed = self._run({"screenshot": ([record()], [broken]), "crash": ([], [])})
        self.assertEqual(code, 1, "an unreadable submission must fail the run")
        self.assertEqual(len(filed), 1, "…without dropping the rows it could read")

    def test_an_unreadable_crash_submission_also_fails_the_run(self):
        broken = tf.MissingAttribute("betaFeedbackCrashSubmissions", "BAD",
                                     "createdDate", {"comment"})
        code, _ = self._run({"screenshot": ([], []), "crash": ([], [broken])})
        self.assertEqual(code, 1)

    def test_missing_credentials_exit_non_zero_without_calling_the_api(self):
        saved = {k: os.environ.get(k) for k in self.ENV}
        for k in self.ENV:
            os.environ.pop(k, None)
        try:
            def boom(*a):
                raise AssertionError("must not mint a token without credentials")
            with Patched(make_jwt=boom), contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(tf.main(), 1)
        finally:
            for k, v in saved.items():
                if v is not None:
                    os.environ[k] = v

    def test_the_crash_log_is_fetched_only_for_a_submission_being_filed(self):
        asked = []

        def crash_log(link, sid, token):
            asked.append(sid)
            return "Thread 0 crashed"

        crash = record(kind="crash", id="CRASH1", screenshots=[],
                       crashLogLink=CRASH_LOG_LINK)
        code, filed = self._run({"screenshot": ([], []), "crash": ([crash], [])},
                                crash_log=crash_log)
        self.assertEqual(code, 0)
        self.assertEqual(asked, ["CRASH1"])
        self.assertIn("Thread 0 crashed", tf.issue_body(filed[0]))

    def test_a_deduped_crash_log_is_never_downloaded(self):
        # One request per log, and every crash ever submitted comes back every
        # run — so an already-filed crash must cost zero extra requests.
        asked = []
        crash = record(kind="crash", id="CRASH1", screenshots=[],
                       crashLogLink=CRASH_LOG_LINK)
        code, filed = self._run(
            {"screenshot": ([], []), "crash": ([crash], [])},
            exists=lambda fid: True,
            crash_log=lambda link, sid, token: asked.append(sid) or "")
        self.assertEqual(code, 0)
        self.assertEqual(filed, [])
        self.assertEqual(asked, [], "downloaded the log of an already-filed crash")

    def test_an_already_filed_submission_is_not_filed_twice(self):
        saved = {k: os.environ.get(k) for k in self.ENV}
        os.environ.update(self.ENV)
        filed = []
        try:
            with Patched(make_jwt=lambda *a: "t",
                         fetch=lambda kind, app, tok: ([record()], []) if kind == "screenshot" else ([], []),
                         issue_exists=lambda fid: True,
                         create_issue=filed.append), \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(tf.main(), 0)
        finally:
            for k, v in saved.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v
        self.assertEqual(filed, [])


class FixtureIntegrityTests(unittest.TestCase):
    """The fixtures must stay real, or every test above proves nothing."""

    def test_fixture_attribute_names_are_the_ones_requested(self):
        # Any fixture key that isn't in FIELDS is a key the API was never asked
        # for, which would mean the fixture drifted away from a real response.
        for key in screenshot_row()["attributes"]:
            self.assertIn(key, tf.FIELDS["screenshot"], f"fixture invented {key!r}")

    def test_fixture_is_json_serialisable(self):
        json.dumps(screenshot_row())


if __name__ == "__main__":
    unittest.main(verbosity=2)
