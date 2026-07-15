#!/usr/bin/env python3
"""Prove the seven-tag set answers EVERY P4/P4.5 slice (AI-Engineer task 3.1) on a live Postgres.

AI-engineer discipline: don't assert sufficiency — demonstrate it with data. This applies the P0
migration, loads a small synthetic result set (2 variants x 2 nodes x 2 cases x 3 seeds), plus the
two DERIVED-label tables a real deployment adds downstream (failure clusters, node->pattern), then
runs each required slice query and prints the grouped results so the reader can recompute them.

Slices required by P4/P4.5:
  per-variant, per-node attribution, per-case, per-seed confidence intervals,
  per-failure-cluster (P4.5 derived label, joined on case_id),
  per-pattern (P3.5 derived label, joined on node_id).

Run via run_pg_proof.sh style env (PGHOST/PGUSER/PGDATABASE). Requires "psycopg[binary]".
"""
import os
import re
import sys
import psycopg

HERE = os.path.dirname(os.path.abspath(__file__))
UP = os.path.join(HERE, "0001_p0_lineage.up.sql")
DOWN = os.path.join(HERE, "0001_p0_lineage.down.sql")
H1 = "1" * 64
H2 = "2" * 64


def exec_script(conn, path):
    sql = re.sub(r"--[^\n]*", "", open(path).read())
    with conn.cursor() as cur:
        for s in (x.strip() for x in sql.split(";")):
            if s:
                cur.execute(s)
    conn.commit()


def load(conn):
    cur = conn.cursor()
    cur.execute("INSERT INTO workflow VALUES('wf1','u','abc123','python','1.0.0',now())")
    cur.execute("INSERT INTO variant VALUES('vA','wf1','A',now()),('vB','wf1','B',now())")
    cur.execute("INSERT INTO config VALUES(%s,'vA','wf1','1.0.0','{}',now())", (H1,))
    cur.execute("INSERT INTO config VALUES(%s,'vB','wf1','1.0.0','{}',now())", (H2,))
    cur.execute("INSERT INTO node VALUES('wf1','n1','static_definition'),('wf1','n2','static_definition')")
    cur.execute("INSERT INTO eval_case VALUES('c1','wf1','default',now()),('c2','wf1','default',now())")

    # Derived-label tables a downstream deployment adds (NOT part of P0; they JOIN on the P0 tags).
    # This is exactly the "close gaps additively" point: derived slices need no new tag, only a join key.
    cur.execute("CREATE TABLE failure_cluster(case_id TEXT PRIMARY KEY REFERENCES eval_case(case_id), cluster TEXT NOT NULL)")
    cur.execute("INSERT INTO failure_cluster VALUES('c1','multi_hop'),('c2','tool_timeout')")
    cur.execute("CREATE TABLE node_pattern(workflow_id TEXT, node_id TEXT, pattern TEXT, PRIMARY KEY(workflow_id,node_id))")
    cur.execute("INSERT INTO node_pattern VALUES('wf1','n1','router'),('wf1','n2','tool_use')")

    # Synthetic metric rows: variants x nodes x cases x 3 seeds, metric = end-to-end 'success' (0/1-ish).
    rows = []
    val = {("vA", "n1"): 0.9, ("vA", "n2"): 0.6, ("vB", "n1"): 0.8, ("vB", "n2"): 0.7}
    for (vid, ch) in (("vA", H1), ("vB", H2)):
        for node in ("n1", "n2"):
            for case in ("c1", "c2"):
                for seed in (1, 2, 3):
                    v = val[(vid, node)] + (0.03 if case == "c2" else 0) + (seed - 2) * 0.01
                    rows.append((ch, vid, f"run_{vid}", node, case, seed,
                                 "2026-07-15T00:00:00Z", "wf1", "success", v, "ratio"))
    cur.executemany(
        "INSERT INTO eval_result(config_hash,variant_id,run_id,node_id,case_id,seed,ts,workflow_id,metric_name,value,unit) "
        "VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)", rows)
    conn.commit()
    return len(rows)


def show(conn, title, sql):
    print(f"\n== {title} ==")
    with conn.cursor() as cur:
        cur.execute(sql)
        cols = [d.name for d in cur.description]
        print("   " + " | ".join(cols))
        n = 0
        for r in cur.fetchall():
            n += 1
            print("   " + " | ".join(f"{x:.4f}" if isinstance(x, float) else str(x) for x in r))
    return n


def main():
    conn = psycopg.connect()
    exec_script(conn, UP)
    n = load(conn)
    print(f"loaded {n} synthetic metric rows (2 variants x 2 nodes x 2 cases x 3 seeds)")

    checks = []

    # per-variant
    checks.append(("per-variant (variant_id / config_hash)",
        "SELECT variant_id, config_hash, avg(value) mean, count(*) FROM eval_result GROUP BY variant_id, config_hash ORDER BY variant_id", 2))
    # per-node attribution
    checks.append(("per-node attribution (node_id)",
        "SELECT variant_id, node_id, avg(value) mean FROM eval_result GROUP BY variant_id, node_id ORDER BY 1,2", 4))
    # per-case
    checks.append(("per-case (case_id)",
        "SELECT case_id, avg(value) mean, count(*) FROM eval_result GROUP BY case_id ORDER BY 1", 2))
    # per-seed confidence interval: mean + stddev across seeds, per (variant,node,case,metric)
    checks.append(("per-seed CI (group across seed => mean, stddev, n)",
        "SELECT variant_id,node_id,case_id, avg(value) mean, coalesce(stddev_samp(value),0) sd, count(distinct seed) seeds "
        "FROM eval_result GROUP BY variant_id,node_id,case_id ORDER BY 1,2,3", 8))
    # per-failure-cluster: DERIVED label, joined on case_id (no new tag needed)
    checks.append(("per-failure-cluster (join failure_cluster ON case_id)",
        "SELECT fc.cluster, avg(er.value) mean, count(*) FROM eval_result er JOIN failure_cluster fc USING(case_id) GROUP BY fc.cluster ORDER BY 1", 2))
    # per-pattern: DERIVED label, joined on node_id (no new tag needed)
    checks.append(("per-pattern (join node_pattern ON node_id)",
        "SELECT np.pattern, avg(er.value) mean, count(*) FROM eval_result er JOIN node_pattern np USING(workflow_id,node_id) GROUP BY np.pattern ORDER BY 1", 2))

    ok = True
    for title, sql, want_rows in checks:
        got = show(conn, title, sql)
        verdict = "ok" if got == want_rows else "FAIL"
        if got != want_rows:
            ok = False
        print(f"   -> {verdict}: {got} groups (expected {want_rows})")

    # "B beat A?" honesty check: same eval set, multi-seed, comparable by config_hash.
    print("\n== headline: variant comparison keyed by config_hash (honest 'B beat A?') ==")
    with conn.cursor() as cur:
        cur.execute("SELECT variant_id, config_hash, avg(value) mean, coalesce(stddev_samp(value),0) sd, count(*) n "
                    "FROM eval_result GROUP BY variant_id, config_hash ORDER BY mean DESC")
        for r in cur.fetchall():
            print(f"   {r[0]} ({r[1][:12]}): mean={r[2]:.4f} sd={r[3]:.4f} n={r[4]}")

    # drop the derived-label tables first (they FK-reference eval_case) so the down migration is clean
    with conn.cursor() as cur:
        cur.execute("DROP TABLE IF EXISTS failure_cluster")
        cur.execute("DROP TABLE IF EXISTS node_pattern")
    conn.commit()
    exec_script(conn, DOWN)
    conn.close()
    print("\n" + ("ALL SLICES ANSWERABLE FROM THE SEVEN-TAG SET (+ derived-label joins)"
                  if ok else "SOME SLICES DID NOT RETURN EXPECTED GROUPS"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
