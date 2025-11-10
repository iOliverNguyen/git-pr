package main

import (
	"strings"
	"testing"
)

func TestParseLogs(t *testing.T) {
	t.Run("parse logs", func(t *testing.T) {
		// Sample logs with 5 commits testing different scenarios:
		// 1. Empty commit (no title, no body) - like jujutsu "jj new"
		// 2. Simple commit (title only, no body)
		// 3. Commit with body and footers (draft/random tags, Remote-Ref, Tags attributes)
		// 4. Commit with simple body (no footers)
		// 5. Commit with emoji in title and multi-paragraph body with multiple sections
		logs := `
commit 4aaaee8852a1aa92ed01ff28c4f40331833f9281
Author: Oliver N <oliver@example.com>
Date:   Mon Dec 31 10:32:05 2025 +0700

commit 2e4d93e3728b7d3baa6ed3d8d56d9e4fbd73422d
Author: Alice M <alice@example.com>
Date:   Fri Nov 30 18:30:16 2025 -0300

    fix: correct typo in documentation

commit 1a3f1e297fec2af1cae6fa5f8d0955e2dfa4b0dc
Author: Oliver N <oliver@example.com>
Date:   Sun Dec 31 9:19:11 2025 +0700

    [draft][random] this is an example commit message

    Summary
    ---

    this is an example commit message

    Remote-Ref: iOliverNguyen/13453619
    Tags: example, testing

commit 8bb40dd65938b9c93b446113a61fe204b02011b8
Author: Aline <aline@example.com>
Date:   Fri Nov 10 18:30:16 2025 -0300

    feat: add new feature to improve performance

    added a new caching layer to reduce latency

commit 2b59e7223f2cb3196fe2ef322ca6c2f205f24285
Author: Oliver Nguyen <oliver@example.com>
Date:   Sun Dec 31 8:02:52 2025 +0700

    🛠️ Introduce a simulated SuperpowerDB backend in unit tests to centralize
    handling of data persistence, in-memory caching, and async queues.

    ## Changes
    - Add "SuperpowerDBMock" class providing unified interfaces for
      "storage", "cache", and "queue"
    - Replace scattered mocks with shared SuperpowerDB fixture
    - Add coverage for concurrent read/write and delayed queue processing
    - Update test utilities to simplify resource cleanup

    ## Why Needed
    Current tests use separate mocks for database, cache, and queue layers,
    leading to duplicated setup logic and inconsistent behavior. A unified
    mock improves maintainability and more accurately reflects production
    integration patterns.

    ## Impact
    - Simplifies test setup and reduces boilerplate
    - Enables end-to-end testing of complex data flows
    - Lays groundwork for benchmarking async persistence behavior

    Remote-Ref: iOliverNguyen/13453620
`
		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		// verify we parsed 5 commits
		assert(t, len(commits) == 5).Fatalf("expected 5 commits, got %d", len(commits))

		// test commit 1: empty title and body (like jujutsu "jj new")
		c1 := commits[0]
		assert(t, c1.AuthorName == "Oliver N").Errorf("commit 1 author name = %q, want %q", c1.AuthorName, "Oliver N")
		assert(t, c1.Title == "").Errorf("commit 1 title = %q, want empty", c1.Title)
		assert(t, c1.Message == "").Errorf("commit 1 message = %q, want empty", c1.Message)

		// test commit 2: simple title only
		c2 := commits[1]
		assert(t, c2.Hash == "2e4d93e3728b7d3baa6ed3d8d56d9e4fbd73422d").Errorf("commit 2 hash = %q", c2.Hash)
		assert(t, c2.Message == "").Errorf("commit 2 message = %q, want empty", c2.Message)
		assert(t, len(c2.Attrs) == 0).Errorf("commit 2 attrs = %v, want empty", c2.Attrs)

		// test commit 3: with body and footers
		c3 := commits[2]
		assert(t, c3.Hash == "1a3f1e297fec2af1cae6fa5f8d0955e2dfa4b0dc").Errorf("commit 3 hash = %q", c3.Hash)
		assert(t, c3.Title == "[draft][random] this is an example commit message").Errorf("commit 3 title = %q", c3.Title)
		expectedMsg := "Summary\n---\n\nthis is an example commit message"
		assert(t, c3.Message == expectedMsg).Errorf("commit 3 message = %q, want %q", c3.Message, expectedMsg)
		// check Remote-Ref attribute
		remoteRef := c3.GetRemoteRef()
		assert(t, remoteRef == "iOliverNguyen/13453619").Errorf("commit 3 remote-ref = %q, want %q", remoteRef, "iOliverNguyen/13453619")
		// check Tags attribute
		tags := c3.GetAttr("tags")
		assert(t, tags == "example, testing").Errorf("commit 3 tags = %q, want %q", tags, "example, testing")

		// test commit 4: simple body without footers
		c4 := commits[3]
		assert(t, c4.Hash == "8bb40dd65938b9c93b446113a61fe204b02011b8").Errorf("commit 4 hash = %q", c4.Hash)
		assert(t, c4.Title == "feat: add new feature to improve performance").Errorf("commit 4 title = %q", c4.Title)
		assert(t, c4.Message == "added a new caching layer to reduce latency").Errorf("commit 4 message = %q", c4.Message)

		// test commit 5: emoji in title and multi-paragraph body
		c5 := commits[4]
		assert(t, c5.Hash == "2b59e7223f2cb3196fe2ef322ca6c2f205f24285").Errorf("commit 5 hash = %q", c5.Hash)
		// Note: title is only the first line
		expectedTitle := "🛠️ Introduce a simulated SuperpowerDB backend in unit tests to centralize"
		assert(t, c5.Title == expectedTitle).Errorf("commit 5 title = %q, want %q", c5.Title, expectedTitle)
		// the second line becomes part of the message
		assert(t, c5.GetRemoteRef() == "iOliverNguyen/13453620").Errorf("commit 5 remote-ref = %q", c5.GetRemoteRef())
		// verify message contains sections
		assert(t, strings.Contains(c5.Message, "## Changes")).Errorf("commit 5 message missing '## Changes' section")
		assert(t, strings.Contains(c5.Message, "## Why Needed")).Errorf("commit 5 message missing '## Why Needed' section")
		assert(t, strings.Contains(c5.Message, "## Impact")).Errorf("commit 5 message missing '## Impact' section")
	})

	t.Run("ParseLogsEmpty", func(t *testing.T) {
		// test empty input
		commits, err := parseLogs("")
		assert(t, err == nil).Fatalf("parseLogs('') error = %v", err)
		assert(t, len(commits) == 0).Errorf("parseLogs('') = %v, want empty", commits)

		// test whitespace only
		commits, err = parseLogs("   \n  \n  ")
		assert(t, err == nil).Fatalf("parseLogs(whitespace) error = %v", err)
		assert(t, len(commits) == 0).Errorf("parseLogs(whitespace) = %v, want empty", commits)
	})

	t.Run("ParseLogsSingleCommit", func(t *testing.T) {
		logs := `commit abc123def456789012345678901234567890abcd
Author: Test User <test@example.com>
Date:   Mon Jan 1 00:00:00 2024 +0000

    test: single commit

    This is a test commit.

    Remote-Ref: testuser/abc123de
`

		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))

		c := commits[0]
		assert(t, c.Hash == "abc123def456789012345678901234567890abcd").Errorf("hash = %q", c.Hash)
		assert(t, c.Title == "test: single commit").Errorf("title = %q", c.Title)
		assert(t, c.Message == "This is a test commit.").Errorf("message = %q", c.Message)
		assert(t, c.GetRemoteRef() == "testuser/abc123de").Errorf("remote-ref = %q", c.GetRemoteRef())
	})

	t.Run("ParseLogsMultipleFooters", func(t *testing.T) {
		logs := `commit abc123def456789012345678901234567890abcd
Author: Test User <test@example.com>
Date:   Mon Jan 1 00:00:00 2024 +0000

    feat: test multiple footers

    This commit has multiple footer attributes.

    Remote-Ref: testuser/abc123de
    Tags: feat, test, example
    Custom-Footer: custom value
    Another-Key: another value
`

		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))

		c := commits[0]
		assert(t, c.GetRemoteRef() == "testuser/abc123de").Errorf("remote-ref = %q", c.GetRemoteRef())
		assert(t, c.GetAttr("tags") == "feat, test, example").Errorf("tags = %q", c.GetAttr("tags"))
		assert(t, c.GetAttr("custom-footer") == "custom value").Errorf("custom-footer = %q", c.GetAttr("custom-footer"))
		assert(t, c.GetAttr("another-key") == "another value").Errorf("another-key = %q", c.GetAttr("another-key"))
		// verify we have 4 attributes
		assert(t, len(c.Attrs) == 4).Errorf("expected 4 attrs, got %d: %v", len(c.Attrs), c.Attrs)
	})

	t.Run("ParseLogsNoBody", func(t *testing.T) {
		logs := `commit abc123def456789012345678901234567890abcd
Author: Test User <test@example.com>
Date:   Mon Jan 1 00:00:00 2024 +0000

    chore: commit with no body
`

		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))

		c := commits[0]
		assert(t, c.Title == "chore: commit with no body").Errorf("title = %q", c.Title)
		assert(t, c.Message == "").Errorf("message = %q, want empty", c.Message)
		assert(t, len(c.Attrs) == 0).Errorf("attrs = %v, want empty", c.Attrs)
	})

	t.Run("ParseLogsAlternativeDateFormat", func(t *testing.T) {
		logs := `commit abc123def456789012345678901234567890abcd
Author: Test User <test@example.com>
Date:   2024-01-01 12:34:56 +0000

    test: alternative date format
`

		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))

		c := commits[0]
		assert(t, !c.Date.IsZero()).Errorf("date is zero, want parsed date")
		// verify date is in UTC
		assert(t, c.Date.Location().String() == "UTC").Errorf("date location = %v, want UTC", c.Date.Location())
	})
}

func TestParseJJWorkingCopy(t *testing.T) {
	t.Run("empty without description", func(t *testing.T) {
		checkOutput := "EMPTY|NO-DESC"
		infoOutput := "abc123|def456|"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil, got %+v", commit)
	})

	t.Run("nonempty without description", func(t *testing.T) {
		checkOutput := "NONEMPTY|NO-DESC"
		infoOutput := "abc123|def456|test"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil, got %+v", commit)
	})

	t.Run("empty with description, allowEmpty=false", func(t *testing.T) {
		checkOutput := "EMPTY|HAS-DESC"
		infoOutput := "abc123|def456|test commit"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil, got %+v", commit)
	})

	t.Run("empty with description, allowEmpty=true", func(t *testing.T) {
		checkOutput := "EMPTY|HAS-DESC"
		infoOutput := "abc123|def456|test commit"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, true)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit != nil).Fatalf("expected commit, got nil")
		assert(t, commit.ChangeID == "abc123").Errorf("changeID = %q", commit.ChangeID)
		assert(t, commit.Hash == "def456").Errorf("hash = %q", commit.Hash)
		assert(t, commit.Title == "test commit").Errorf("title = %q", commit.Title)
		assert(t, commit.Message == "").Errorf("message = %q, want empty", commit.Message)
	})

	t.Run("nonempty with description", func(t *testing.T) {
		checkOutput := "NONEMPTY|HAS-DESC"
		infoOutput := "change123|commit456|feat: add new feature"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit != nil).Fatalf("expected commit, got nil")
		assert(t, commit.ChangeID == "change123").Errorf("changeID = %q", commit.ChangeID)
		assert(t, commit.Hash == "commit456").Errorf("hash = %q", commit.Hash)
		assert(t, commit.Title == "feat: add new feature").Errorf("title = %q", commit.Title)
		assert(t, commit.Message == "").Errorf("message = %q, want empty", commit.Message)
	})

	t.Run("multi-line description with body", func(t *testing.T) {
		checkOutput := "NONEMPTY|HAS-DESC"
		infoOutput := `change123|commit456|fix: resolve bug

This is a detailed explanation
of the bug fix.`
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit != nil).Fatalf("expected commit, got nil")
		assert(t, commit.Title == "fix: resolve bug").Errorf("title = %q", commit.Title)
		assert(t, commit.Message == "This is a detailed explanation\nof the bug fix.").Errorf("message = %q", commit.Message)
	})

	t.Run("description with footers", func(t *testing.T) {
		checkOutput := "NONEMPTY|HAS-DESC"
		infoOutput := `change123|commit456|feat: implement feature

Description of the feature.

    Remote-Ref: user/abc123
    Tags: feature, test`
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit != nil).Fatalf("expected commit, got nil")
		assert(t, commit.Title == "feat: implement feature").Errorf("title = %q", commit.Title)
		assert(t, commit.Message == "Description of the feature.").Errorf("message = %q", commit.Message)
		assert(t, commit.GetRemoteRef() == "user/abc123").Errorf("remote-ref = %q", commit.GetRemoteRef())
		assert(t, commit.GetAttr("tags") == "feature, test").Errorf("tags = %q", commit.GetAttr("tags"))
	})

	t.Run("invalid format - wrong parts count", func(t *testing.T) {
		checkOutput := "NONEMPTY|HAS-DESC"
		infoOutput := "onlyonepart"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err != nil).Errorf("expected error, got nil")
		assert(t, commit == nil).Errorf("expected nil commit on error")
	})

	t.Run("invalid checkOutput format", func(t *testing.T) {
		checkOutput := "INVALID"
		infoOutput := "change123|commit456|title"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput, false)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil for invalid format")
	})
}
