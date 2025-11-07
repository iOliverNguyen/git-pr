package main

import (
	"testing"
)

func TestParseLogs(t *testing.T) {
	// sample logs: empty title, empty body, multi-line body, emojis, draft/random tags, Remote-Ref and Tags footers
	logs := `
commit 4aaaee8852a1aa92ed01ff28c4f40331833f9281 (HEAD)
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
}
