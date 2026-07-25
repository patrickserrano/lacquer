---
title: "supabase-postgres-best-practices"
description: "Use when writing, reviewing, or optimizing Postgres queries, schema designs, or database configurations — including indexing, connection pooling, RLS policies, and locking. Postgres performance best practices from Supabase."
---

:::note
Generated from [`profiles/supabase/skills/supabase-postgres-best-practices/SKILL.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/supabase/skills/supabase-postgres-best-practices/SKILL.md) — edit that file, not this page.
:::


# Supabase Postgres Best Practices

Comprehensive performance optimization guide for Postgres, maintained by Supabase. Contains rules across 8 categories, prioritized by impact to guide automated query optimization and schema design.

## When to Apply

Reference these guidelines when:
- Writing SQL queries or designing schemas
- Implementing indexes or query optimization
- Reviewing database performance issues
- Configuring connection pooling or scaling
- Optimizing for Postgres-specific features
- Working with Row-Level Security (RLS)

## Rule Categories by Priority

| Priority | Category | Impact | Prefix |
|----------|----------|--------|--------|
| 1 | Query Performance | CRITICAL | `query-` |
| 2 | Connection Management | CRITICAL | `conn-` |
| 3 | Security & RLS | CRITICAL | `security-` |
| 4 | Schema Design | HIGH | `schema-` |
| 5 | Concurrency & Locking | MEDIUM-HIGH | `lock-` |
| 6 | Data Access Patterns | MEDIUM | `data-` |
| 7 | Monitoring & Diagnostics | LOW-MEDIUM | `monitor-` |
| 8 | Advanced Features | LOW | `advanced-` |

## How to Use

Read individual rule files for detailed explanations and SQL examples:

```
references/query-missing-indexes.md
references/query-partial-indexes.md
references/_sections.md
```

Each rule file contains:
- Brief explanation of why it matters
- Incorrect SQL example with explanation
- Correct SQL example with explanation
- Optional EXPLAIN output or metrics
- Additional context and references
- Supabase-specific notes (when applicable)

## References

- https://www.postgresql.org/docs/current/
- https://supabase.com/docs
- https://wiki.postgresql.org/wiki/Performance_Optimization
- https://supabase.com/docs/guides/database/overview
- https://supabase.com/docs/guides/auth/row-level-security

Source: [supabase/agent-skills](https://github.com/supabase/agent-skills/tree/main/skills/supabase-postgres-best-practices), official, MIT-licensed.


## Reference files

- [Section Definitions](/lacquer/skills/supabase/supabase-postgres-best-practices/_sections/)
- [Advanced Full Text Search](/lacquer/skills/supabase/supabase-postgres-best-practices/advanced-full-text-search/)
- [Advanced Jsonb Indexing](/lacquer/skills/supabase/supabase-postgres-best-practices/advanced-jsonb-indexing/)
- [pgbouncer.ini](/lacquer/skills/supabase/supabase-postgres-best-practices/conn-idle-timeout/)
- [Conn Limits](/lacquer/skills/supabase/supabase-postgres-best-practices/conn-limits/)
- [Conn Pooling](/lacquer/skills/supabase/supabase-postgres-best-practices/conn-pooling/)
- [Conn Prepared Statements](/lacquer/skills/supabase/supabase-postgres-best-practices/conn-prepared-statements/)
- [Data Batch Inserts](/lacquer/skills/supabase/supabase-postgres-best-practices/data-batch-inserts/)
- [Data N Plus One](/lacquer/skills/supabase/supabase-postgres-best-practices/data-n-plus-one/)
- [Data Pagination](/lacquer/skills/supabase/supabase-postgres-best-practices/data-pagination/)
- [Data Upsert](/lacquer/skills/supabase/supabase-postgres-best-practices/data-upsert/)
- [Lock Advisory](/lacquer/skills/supabase/supabase-postgres-best-practices/lock-advisory/)
- [Lock Deadlock Prevention](/lacquer/skills/supabase/supabase-postgres-best-practices/lock-deadlock-prevention/)
- [Lock Short Transactions](/lacquer/skills/supabase/supabase-postgres-best-practices/lock-short-transactions/)
- [Lock Skip Locked](/lacquer/skills/supabase/supabase-postgres-best-practices/lock-skip-locked/)
- [Monitor Explain Analyze](/lacquer/skills/supabase/supabase-postgres-best-practices/monitor-explain-analyze/)
- [Monitor Pg Stat Statements](/lacquer/skills/supabase/supabase-postgres-best-practices/monitor-pg-stat-statements/)
- [Monitor Vacuum Analyze](/lacquer/skills/supabase/supabase-postgres-best-practices/monitor-vacuum-analyze/)
- [Query Composite Indexes](/lacquer/skills/supabase/supabase-postgres-best-practices/query-composite-indexes/)
- [Query Covering Indexes](/lacquer/skills/supabase/supabase-postgres-best-practices/query-covering-indexes/)
- [Query Index Types](/lacquer/skills/supabase/supabase-postgres-best-practices/query-index-types/)
- [Query Missing Indexes](/lacquer/skills/supabase/supabase-postgres-best-practices/query-missing-indexes/)
- [Query Partial Indexes](/lacquer/skills/supabase/supabase-postgres-best-practices/query-partial-indexes/)
- [Schema Constraints](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-constraints/)
- [Schema Data Types](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-data-types/)
- [Schema Foreign Key Indexes](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-foreign-key-indexes/)
- [Schema Lowercase Identifiers](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-lowercase-identifiers/)
- [Schema Partitioning](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-partitioning/)
- [Schema Primary Keys](/lacquer/skills/supabase/supabase-postgres-best-practices/schema-primary-keys/)
- [Security Privileges](/lacquer/skills/supabase/supabase-postgres-best-practices/security-privileges/)
- [Security Rls Basics](/lacquer/skills/supabase/supabase-postgres-best-practices/security-rls-basics/)
- [Security Rls Performance](/lacquer/skills/supabase/supabase-postgres-best-practices/security-rls-performance/)
