package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseLogs(t *testing.T) {
	t.Run("parse logs", func(t *testing.T) {
		// Sample logs with 4 commits testing different scenarios:
		// 1. Simple commit (title only, no body)
		// 2. Commit with body and footers (draft/random tags, Remote-Ref, Tags attributes)
		// 3. Commit with simple body (no footers)
		// 4. Commit with emoji in title and multi-paragraph body with multiple sections
		// Note: empty commits (no title and no message) are filtered out
		logs := `
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
		// verify we parsed 4 commits
		assert(t, len(commits) == 4).Fatalf("expected 4 commits, got %d", len(commits))

		// test commit 1: simple title only
		c1 := commits[0]
		assert(t, c1.Hash == "2e4d93e3728b7d3baa6ed3d8d56d9e4fbd73422d").Errorf("commit 1 hash = %q", c1.Hash)
		assert(t, c1.Message == "").Errorf("commit 1 message = %q, want empty", c1.Message)
		assert(t, len(c1.Attrs) == 0).Errorf("commit 1 attrs = %v, want empty", c1.Attrs)

		// test commit 2: with body and footers
		c2 := commits[1]
		assert(t, c2.Hash == "1a3f1e297fec2af1cae6fa5f8d0955e2dfa4b0dc").Errorf("commit 2 hash = %q", c2.Hash)
		assert(t, c2.Title == "[draft][random] this is an example commit message").Errorf("commit 2 title = %q", c2.Title)
		expectedMsg := "Summary\n---\n\nthis is an example commit message"
		assert(t, c2.Message == expectedMsg).Errorf("commit 2 message = %q, want %q", c2.Message, expectedMsg)
		// check Remote-Ref attribute
		remoteRef := c2.GetRemoteRef()
		assert(t, remoteRef == "iOliverNguyen/13453619").Errorf("commit 2 remote-ref = %q, want %q", remoteRef, "iOliverNguyen/13453619")
		// check Tags attribute
		tags := c2.GetAttr("tags")
		assert(t, tags == "example, testing").Errorf("commit 2 tags = %q, want %q", tags, "example, testing")

		// test commit 3: simple body without footers
		c3 := commits[2]
		assert(t, c3.Hash == "8bb40dd65938b9c93b446113a61fe204b02011b8").Errorf("commit 3 hash = %q", c3.Hash)
		assert(t, c3.Title == "feat: add new feature to improve performance").Errorf("commit 3 title = %q", c3.Title)
		assert(t, c3.Message == "added a new caching layer to reduce latency").Errorf("commit 3 message = %q", c3.Message)

		// test commit 4: emoji in title and multi-paragraph body
		c4 := commits[3]
		assert(t, c4.Hash == "2b59e7223f2cb3196fe2ef322ca6c2f205f24285").Errorf("commit 4 hash = %q", c4.Hash)
		// Note: title is only the first line
		expectedTitle := "🛠️ Introduce a simulated SuperpowerDB backend in unit tests to centralize"
		assert(t, c4.Title == expectedTitle).Errorf("commit 4 title = %q, want %q", c4.Title, expectedTitle)
		// the second line becomes part of the message
		assert(t, c4.GetRemoteRef() == "iOliverNguyen/13453620").Errorf("commit 4 remote-ref = %q", c4.GetRemoteRef())
		// verify message contains sections
		assert(t, strings.Contains(c4.Message, "## Changes")).Errorf("commit 4 message missing '## Changes' section")
		assert(t, strings.Contains(c4.Message, "## Why Needed")).Errorf("commit 4 message missing '## Why Needed' section")
		assert(t, strings.Contains(c4.Message, "## Impact")).Errorf("commit 4 message missing '## Impact' section")
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

	t.Run("ParseLogsTitleEmptyBodyWithFooter", func(t *testing.T) {
		logs := `commit def456abc123789012345678901234567890abcd
Author: Test User <test@example.com>
Date:   Mon Jan 1 00:00:00 2024 +0000

    feat: test empty body with footer

    Remote-Ref: testuser/abc123de
`

		commits, err := parseLogs(logs)
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))

		c := commits[0]
		assert(t, c.Hash == "def456abc123789012345678901234567890abcd").Errorf("hash = %q", c.Hash)
		assert(t, c.Title == "feat: test empty body with footer").Errorf("title = %q", c.Title)
		assert(t, c.Message == "").Errorf("message = %q, want empty", c.Message)
		assert(t, c.GetRemoteRef() == "testuser/abc123de").Errorf("remote-ref = %q", c.GetRemoteRef())
		assert(t, len(c.Attrs) == 1).Errorf("expected 1 attr, got %d: %v", len(c.Attrs), c.Attrs)
	})
}

func TestParseJJWorkingCopy(t *testing.T) {
	t.Run("empty without description", func(t *testing.T) {
		checkOutput := "EMPTY|NO-DESC"
		infoOutput := "abc123|def456|"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil, got %+v", commit)
	})

	t.Run("nonempty without description", func(t *testing.T) {
		checkOutput := "NONEMPTY|NO-DESC"
		infoOutput := "abc123|def456|test"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil, got %+v", commit)
	})

	t.Run("empty with description", func(t *testing.T) {
		checkOutput := "EMPTY|HAS-DESC"
		infoOutput := "abc123|def456|test commit"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil for empty commit, got %+v", commit)
	})

	t.Run("nonempty with description", func(t *testing.T) {
		checkOutput := "NONEMPTY|HAS-DESC"
		infoOutput := "change123|commit456|feat: add new feature"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
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
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
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
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
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
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
		assert(t, err != nil).Errorf("expected error, got nil")
		assert(t, commit == nil).Errorf("expected nil commit on error")
	})

	t.Run("invalid checkOutput format", func(t *testing.T) {
		checkOutput := "INVALID"
		infoOutput := "change123|commit456|title"
		commit, err := parseJJWorkingCopy(checkOutput, infoOutput)
		assert(t, err == nil).Fatalf("error = %v", err)
		assert(t, commit == nil).Errorf("expected nil for invalid format")
	})
}

func TestFindPrevNonSkipped(t *testing.T) {
	// build helpers — each commit identified by Hash so failures are readable
	mk := func(hash string, skip bool) *Commit {
		return &Commit{Hash: hash + "00000000", Skip: skip}
	}

	t.Run("empty stack", func(t *testing.T) {
		target := mk("a1", false)
		assert(t, findPrevNonSkipped(nil, target) == nil).Errorf("want nil for empty stack")
	})

	t.Run("target not in stack", func(t *testing.T) {
		a, b := mk("a1", false), mk("b1", false)
		stack := []*Commit{a, b}
		other := mk("c1", false)
		assert(t, findPrevNonSkipped(stack, other) == nil).Errorf("want nil when target missing")
	})

	t.Run("single commit, no predecessor", func(t *testing.T) {
		a := mk("a1", false)
		stack := []*Commit{a}
		assert(t, findPrevNonSkipped(stack, a) == nil).Errorf("want nil; only commit is target")
	})

	t.Run("all predecessors skipped", func(t *testing.T) {
		a, b, c := mk("a1", true), mk("b1", true), mk("c1", false)
		stack := []*Commit{a, b, c}
		assert(t, findPrevNonSkipped(stack, c) == nil).Errorf("want nil; all predecessors skipped")
	})

	t.Run("immediate non-skipped predecessor", func(t *testing.T) {
		a, b := mk("a1", false), mk("b1", false)
		stack := []*Commit{a, b}
		got := findPrevNonSkipped(stack, b)
		assert(t, got == a).Errorf("want a, got %v", got)
	})

	t.Run("regression: skip-non-skip-skip-non-skip-target picks the latest non-skip", func(t *testing.T) {
		// stack [A(skip), B, C, D, E] processing E should pick D, not B.
		// the bug at main.go:222-228 picked B (first non-skip from the start).
		a := mk("a1", true)
		b := mk("b1", false)
		c := mk("c1", false)
		d := mk("d1", false)
		e := mk("e1", false)
		stack := []*Commit{a, b, c, d, e}
		got := findPrevNonSkipped(stack, e)
		assert(t, got == d).Errorf("want d (most recent non-skip), got %v", got)
	})

	t.Run("intermediate skip is hopped over", func(t *testing.T) {
		// stack [A, B(skip), C] — for C, the predecessor should be A.
		a, b, c := mk("a1", false), mk("b1", true), mk("c1", false)
		stack := []*Commit{a, b, c}
		got := findPrevNonSkipped(stack, c)
		assert(t, got == a).Errorf("want a (skipping b), got %v", got)
	})
}

func TestRecoverRangeTip(t *testing.T) {
	t.Run("jj mode resolves tip by change-id and never uses depth", func(t *testing.T) {
		// regression: in jj mode an explicit BASE..TIP must be recovered from the
		// tip's stable change-id, NOT from @-+depth (which pushed the @- sibling
		// stack and ignored the positional range).
		const wantTip = "atipcommithash00000000000000000000000000"
		changeIDCalledWith := ""
		resolveChangeID := func(id string) (string, error) {
			changeIDCalledWith = id
			return wantTip, nil
		}
		resolveDepth := func(head string, depth int) (string, error) {
			t.Fatalf("depth resolver must not be called in jj mode (head=%q depth=%d)", head, depth)
			return "", nil
		}
		got, err := recoverRangeTip(true, "tipchangeid", "ctipfromat-", 6, resolveChangeID, resolveDepth)
		assert(t, err == nil).Fatalf("unexpected error: %v", err)
		assert(t, got == wantTip).Errorf("tip = %q, want %q", got, wantTip)
		assert(t, changeIDCalledWith == "tipchangeid").Errorf("change-id resolver called with %q, want %q", changeIDCalledWith, "tipchangeid")
	})

	t.Run("non-jj mode resolves tip by depth-from-HEAD and never uses change-id", func(t *testing.T) {
		// git-branchless: reword moves HEAD, so depth-from-HEAD is the correct,
		// stable identity; the change-id resolver must not be touched.
		const wantTip = "depthrecoveredhash0000000000000000000000"
		var gotHead string
		var gotDepth int
		resolveChangeID := func(id string) (string, error) {
			t.Fatalf("change-id resolver must not be called in non-jj mode (id=%q)", id)
			return "", nil
		}
		resolveDepth := func(head string, depth int) (string, error) {
			gotHead, gotDepth = head, depth
			return wantTip, nil
		}
		got, err := recoverRangeTip(false, "ignored-change-id", "HEAD", 3, resolveChangeID, resolveDepth)
		assert(t, err == nil).Fatalf("unexpected error: %v", err)
		assert(t, got == wantTip).Errorf("tip = %q, want %q", got, wantTip)
		assert(t, gotHead == "HEAD").Errorf("depth resolver head = %q, want %q", gotHead, "HEAD")
		assert(t, gotDepth == 3).Errorf("depth resolver depth = %d, want 3", gotDepth)
	})

	t.Run("propagates resolver error", func(t *testing.T) {
		wantErr := errorf("boom")
		resolveChangeID := func(string) (string, error) { return "", wantErr }
		resolveDepth := func(string, int) (string, error) { t.Fatal("unused"); return "", nil }
		_, err := recoverRangeTip(true, "id", "head", 1, resolveChangeID, resolveDepth)
		assert(t, err == wantErr).Errorf("err = %v, want %v", err, wantErr)
	})
}

func TestOrderSelection(t *testing.T) {
	// stack ordered oldest→newest: A B C D E
	mk := func(hash string) *Commit { return &Commit{Hash: hash} }
	a, b, c, d, e := mk("a"), mk("b"), mk("c"), mk("d"), mk("e")
	stack := []*Commit{a, b, c, d, e}

	t.Run("contiguous selection", func(t *testing.T) {
		lo, hi, err := orderSelection(stack, []string{"b", "c"})
		assert(t, err == nil).Errorf("unexpected error: %v", err)
		assert(t, lo == 1 && hi == 2).Errorf("lo=%d hi=%d, want 1,2", lo, hi)
	})

	t.Run("gapped selection spans the gap", func(t *testing.T) {
		// selecting B and D leaves C in the span (to be skipped by the caller)
		lo, hi, err := orderSelection(stack, []string{"b", "d"})
		assert(t, err == nil).Errorf("unexpected error: %v", err)
		assert(t, lo == 1 && hi == 3).Errorf("lo=%d hi=%d, want 1,3", lo, hi)
	})

	t.Run("order of args does not matter", func(t *testing.T) {
		lo, hi, err := orderSelection(stack, []string{"e", "a", "c"})
		assert(t, err == nil).Errorf("unexpected error: %v", err)
		assert(t, lo == 0 && hi == 4).Errorf("lo=%d hi=%d, want 0,4", lo, hi)
	})

	t.Run("single commit", func(t *testing.T) {
		lo, hi, err := orderSelection(stack, []string{"c"})
		assert(t, err == nil).Errorf("unexpected error: %v", err)
		assert(t, lo == 2 && hi == 2).Errorf("lo=%d hi=%d, want 2,2", lo, hi)
	})

	t.Run("unknown commit errors", func(t *testing.T) {
		_, _, err := orderSelection(stack, []string{"b", "z"})
		assert(t, err != nil).Errorf("want error for hash not in stack")
	})

	t.Run("empty selection errors", func(t *testing.T) {
		_, _, err := orderSelection(stack, nil)
		assert(t, err != nil).Errorf("want error for empty selection")
	})
}

func TestMarkUnselected(t *testing.T) {
	mk := func(hash string) *Commit { return &Commit{Hash: hash, Title: hash} }

	t.Run("skips commits not in the selected set", func(t *testing.T) {
		a, b, c := mk("a"), mk("b"), mk("c")
		markUnselected([]*Commit{a, b, c}, map[string]bool{"a": true, "c": true}, false)
		assert(t, !a.Skip).Errorf("a should not be skipped")
		assert(t, b.Skip).Errorf("b should be skipped (not selected)")
		assert(t, !c.Skip).Errorf("c should not be skipped")
	})

	t.Run("leaves already-skipped commits skipped", func(t *testing.T) {
		a := &Commit{Hash: "a", Skip: true}
		markUnselected([]*Commit{a}, map[string]bool{"a": true}, false)
		assert(t, a.Skip).Errorf("pre-skipped commit should remain skipped")
	})
}

func TestValidateRemoteRefsBeforePush(t *testing.T) {
	mk := func(hash string, skip bool, ref string) *Commit {
		c := &Commit{Hash: hash + "00000000", Skip: skip}
		if ref != "" {
			c.SetAttr(KeyRemoteRef, ref)
		}
		return c
	}

	t.Run("empty slice returns nil", func(t *testing.T) {
		got := validateRemoteRefsBeforePush(nil)
		assert(t, got == nil).Errorf("want nil, got %v", got)
	})

	t.Run("all have refs returns nil", func(t *testing.T) {
		stack := []*Commit{
			mk("a1", false, "user/a1"),
			mk("b1", false, "user/b1"),
		}
		got := validateRemoteRefsBeforePush(stack)
		assert(t, got == nil).Errorf("want nil, got %v", got)
	})

	t.Run("one missing returns that shorthash", func(t *testing.T) {
		stack := []*Commit{
			mk("a1", false, "user/a1"),
			mk("b1", false, ""), // missing
			mk("c1", false, "user/c1"),
		}
		got := validateRemoteRefsBeforePush(stack)
		assert(t, len(got) == 1).Fatalf("want 1, got %v", got)
		assert(t, got[0] == "b1000000").Errorf("want b1000000, got %v", got[0])
	})

	t.Run("skipped missing-ref is not reported", func(t *testing.T) {
		stack := []*Commit{
			mk("a1", false, "user/a1"),
			mk("b1", true, ""),  // skipped, no ref — should be ignored
			mk("c1", false, ""), // not skipped, no ref — should be reported
		}
		got := validateRemoteRefsBeforePush(stack)
		assert(t, len(got) == 1).Fatalf("want 1, got %v", got)
		assert(t, got[0] == "c1000000").Errorf("want c1000000, got %v", got[0])
	})

	t.Run("multiple missing reported in stack order", func(t *testing.T) {
		stack := []*Commit{
			mk("a1", false, ""),
			mk("b1", false, "user/b1"),
			mk("c1", false, ""),
			mk("d1", false, ""),
		}
		got := validateRemoteRefsBeforePush(stack)
		assert(t, len(got) == 3).Fatalf("want 3, got %v", got)
		assert(t, got[0] == "a1000000" && got[1] == "c1000000" && got[2] == "d1000000").Errorf("got %v", got)
	})
}

func TestShortenTitle(t *testing.T) {
	t.Run("short title unchanged", func(t *testing.T) {
		title := "fix: bug"
		result := shortenTitle(title)
		assert(t, result == title).Errorf("shortenTitle(%q) = %q, want %q", title, result, title)
	})

	t.Run("exact max length unchanged", func(t *testing.T) {
		title := "fix: this is exactly thirty-six!"
		result := shortenTitle(title)
		assert(t, result == title).Errorf("shortenTitle(%q) = %q, want %q", title, result, title)
	})

	t.Run("long title with space", func(t *testing.T) {
		title := "feat: add a very long feature that exceeds the maximum length"
		result := shortenTitle(title)
		assert(t, len(result) <= 40).Errorf("result too long: %d chars", len(result))
		assert(t, strings.HasSuffix(result, " ...")).Errorf("should end with ' ...': %q", result)
	})

	t.Run("long title without space", func(t *testing.T) {
		title := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		result := shortenTitle(title)
		assert(t, len(result) == 39).Errorf("result length = %d, want 39", len(result))
		assert(t, strings.HasSuffix(result, "...")).Errorf("should end with '...': %q", result)
	})
}

// TestFullMessageRoundTrip guards the render/parse round trip: FullMessage right-aligns
// trailer keys, so a footer with 2+ items indents the shorter keys. parseTrailers must
// still recognize them, otherwise every attribute (including Remote-Ref) is dropped and
// git-pr appends a duplicate Remote-Ref on the next push.
func TestFullMessageRoundTrip(t *testing.T) {
	roundTrip := func(t *testing.T, commit *Commit) *Commit {
		t.Helper()
		var b strings.Builder
		b.WriteString("commit abc123def456789012345678901234567890abcd\n")
		b.WriteString("Author: Test User <test@example.com>\n")
		b.WriteString("Date:   Mon Jan 1 00:00:00 2024 +0000\n\n")
		for _, line := range strings.Split(commit.FullMessage(), "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("    " + line + "\n")
		}
		commits, err := parseLogs(b.String())
		assert(t, err == nil).Fatalf("parseLogs() error = %v", err)
		assert(t, len(commits) == 1).Fatalf("expected 1 commit, got %d", len(commits))
		return commits[0]
	}

	t.Run("two footer items", func(t *testing.T) {
		commit := &Commit{
			Title: "wip: something",
			Attrs: []KeyVal{{"wip", "resolve conflict"}, {KeyRemoteRef, "iOliverNguyen/1d074db6"}},
		}
		got := roundTrip(t, commit)
		assert(t, got.GetRemoteRef() == "iOliverNguyen/1d074db6").Errorf("remote-ref = %q", got.GetRemoteRef())
		assert(t, got.GetAttr("wip") == "resolve conflict").Errorf("wip = %q", got.GetAttr("wip"))
		assert(t, got.Message == "").Errorf("message = %q, want empty", got.Message)
	})

	t.Run("many footer items with body", func(t *testing.T) {
		commit := &Commit{
			Title:   "feat: something",
			Message: "A body paragraph.\n\nAnd another one.",
			Attrs: []KeyVal{
				{"wip", "resolve conflict"},
				{KeyTags, "feat, test"},
				{"another-longer-key", "another value"},
				{KeyRemoteRef, "iOliverNguyen/1d074db6"},
			},
		}
		got := roundTrip(t, commit)
		assert(t, len(got.Attrs) == 4).Errorf("expected 4 attrs, got %d: %v", len(got.Attrs), got.Attrs)
		assert(t, got.GetRemoteRef() == "iOliverNguyen/1d074db6").Errorf("remote-ref = %q", got.GetRemoteRef())
		assert(t, got.GetAttr("wip") == "resolve conflict").Errorf("wip = %q", got.GetAttr("wip"))
		assert(t, got.GetAttr(KeyTags) == "feat, test").Errorf("tags = %q", got.GetAttr(KeyTags))
		assert(t, got.GetAttr("another-longer-key") == "another value").Errorf("another-longer-key = %q", got.GetAttr("another-longer-key"))
		assert(t, got.Message == "A body paragraph.\n\nAnd another one.").Errorf("message = %q", got.Message)
	})

	t.Run("idempotent rewrite", func(t *testing.T) {
		commit := &Commit{
			Title: "wip: something",
			Attrs: []KeyVal{{"wip", "resolve conflict"}, {KeyRemoteRef, "iOliverNguyen/1d074db6"}},
		}
		first := roundTrip(t, commit)
		second := roundTrip(t, first)
		assert(t, first.FullMessage() == second.FullMessage()).
			Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first.FullMessage(), second.FullMessage())
	})
}

func TestParseTrailers(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		message string
		attrs   []KeyVal
	}{
		{
			name:    "aligned footer block",
			lines:   []string{"", "       Wip: resolve conflict", "Remote-Ref: user/abc123de"},
			message: "",
			attrs:   []KeyVal{{"remote-ref", "user/abc123de"}, {"wip", "resolve conflict"}},
		},
		{
			name:    "aligned footer after body",
			lines:   []string{"", "The body.", "", "       Wip: resolve conflict", "Remote-Ref: user/abc123de"},
			message: "The body.",
			attrs:   []KeyVal{{"remote-ref", "user/abc123de"}, {"wip", "resolve conflict"}},
		},
		{
			name:    "whitespace-only separator line",
			lines:   []string{"The body.", "   ", "Tags: feat", "Remote-Ref: user/abc123de"},
			message: "The body.",
			attrs:   []KeyVal{{"remote-ref", "user/abc123de"}, {"tags", "feat"}},
		},
		{
			name:    "footer only",
			lines:   []string{"", "Remote-Ref: user/abc123de"},
			message: "",
			attrs:   []KeyVal{{"remote-ref", "user/abc123de"}},
		},
		{
			name:    "no blank line above footer",
			lines:   []string{"The body.", "Remote-Ref: user/abc123de"},
			message: "The body.\nRemote-Ref: user/abc123de",
			attrs:   nil,
		},
		{
			name:    "no footer",
			lines:   []string{"", "The body.", "", "Another paragraph."},
			message: "The body.\n\nAnother paragraph.",
			attrs:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, attrs := parseTrailers(tt.lines)
			assert(t, message == tt.message).Errorf("message = %q, want %q", message, tt.message)
			assert(t, fmt.Sprint(attrs) == fmt.Sprint(tt.attrs)).Errorf("attrs = %v, want %v", attrs, tt.attrs)
		})
	}
}

func TestFullMessage(t *testing.T) {
	t.Run("aligns keys and puts remote-ref last", func(t *testing.T) {
		commit := &Commit{
			Title:   "feat: something",
			Message: "The body.",
			Attrs:   []KeyVal{{KeyRemoteRef, "user/abc123de"}, {"wip", "resolve conflict"}, {KeyTags, "feat"}},
		}
		want := `feat: something

The body.

      Tags: feat
       Wip: resolve conflict
Remote-Ref: user/abc123de`
		assert(t, commit.FullMessage() == want).Errorf("FullMessage() =\n%s\n\nwant:\n%s", commit.FullMessage(), want)
	})

	t.Run("empty message has a single blank line before the footer", func(t *testing.T) {
		commit := &Commit{
			Title: "feat: something",
			Attrs: []KeyVal{{KeyRemoteRef, "user/abc123de"}, {"wip", "resolve conflict"}},
		}
		want := `feat: something

       Wip: resolve conflict
Remote-Ref: user/abc123de`
		assert(t, commit.FullMessage() == want).Errorf("FullMessage() =\n%s\n\nwant:\n%s", commit.FullMessage(), want)
	})
}

func TestDecideRefSync(t *testing.T) {
	const (
		old    = "1111111111111111111111111111111111111111"
		pushed = "2222222222222222222222222222222222222222"
	)
	cases := []struct {
		name        string
		local       string
		fastForward bool
		sameChange  bool
		want        refSyncAction
	}{
		{"already there", pushed, false, false, refSyncNothing},
		{"already there, ancestry irrelevant", pushed, true, true, refSyncNothing},
		// the bug this exists for: the rewrite phase re-created the change, the
		// bookmark still names the pre-rewrite commit
		{"same change, sideways", old, false, true, refSyncMove},
		{"plain fast-forward", old, true, false, refSyncMove},
		// a Remote-Ref hand-written to a name the user already uses, with their
		// branch ahead of the commit we pushed: moving would drop commits
		{"unrelated branch sharing the name", old, false, false, refSyncUnrelated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideRefSync(tc.local, pushed, tc.fastForward, tc.sameChange)
			if got != tc.want {
				t.Errorf("decideRefSync(%v, %v, ff=%v, same=%v) = %v, want %v",
					shortHash(tc.local), shortHash(pushed), tc.fastForward, tc.sameChange, got, tc.want)
			}
		})
	}
}

func TestShortHash(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"abc":          "abc",
		"1234567890ab": "1234567890ab",
		"1111111111111111111111111111111111111111": "111111111111",
	}
	for in, want := range cases {
		if got := shortHash(in); got != want {
			t.Errorf("shortHash(%q) = %q, want %q", in, got, want)
		}
	}
}
