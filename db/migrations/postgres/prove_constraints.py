#!/usr/bin/env python3
"""Apply 0001_p0_lineage and PROVE the constraints fire on a real Postgres (task 2.1 verification).

This is not a unit test of app code; it exercises the DDL against a live server: apply the up
migration, seed valid reference rows, then deliberately attempt each violation and assert the DB
REJECTS it. Finally run the down migration and assert the tables are gone.

Run indirectly via db/migrations/postgres/run_pg_proof.sh (which boots an ephemeral cluster and sets
PGHOST/PGUSER/PGDATABASE). Requires: pip install "psycopg[binary]".
"""
import os
import re
import sys
import psycopg

HERE = os.path.dirname(os.path.abspath(__file__))
UP = os.path.join(HERE, "0001_p0_lineage.up.sql")
DOWN = os.path.join(HERE, "0001_p0_lineage.down.sql")

GOOD_HASH = "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0"
GOOD_HASH2 = "aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999"


def exec_script(conn, path):
    """Execute a multi-statement SQL file. Splits on ';' at top level (the DDL has no dollar-quotes
    or semicolons inside string/identifier literals)."""
    sql = open(path).read()
    # strip full-line and inline -- comments so the naive split is safe
    sql = re.sub(r"--[^\n]*", "", sql)
    stmts = [s.strip() for s in sql.split(";") if s.strip()]
    with conn.cursor() as cur:
        for s in stmts:
            cur.execute(s)
    conn.commit()


def expect_ok(conn, label, sql, params=None):
    try:
        with conn.cursor() as cur:
            cur.execute(sql, params or ())
        conn.commit()
        print(f"ok   ACCEPTED (as intended): {label}")
        return True
    except Exception as e:  # noqa
        conn.rollback()
        print(f"FAIL expected accept but REJECTED: {label} -> {e}")
        return False


def expect_reject(conn, label, want, sql, params=None):
    """Assert the statement is rejected, and that the error is the KIND we expect (want in errno name)."""
    try:
        with conn.cursor() as cur:
            cur.execute(sql, params or ())
        conn.commit()
        print(f"FAIL expected reject but ACCEPTED: {label}")
        return False
    except psycopg.Error as e:
        conn.rollback()
        code = e.sqlstate
        # 23502 not_null_violation, 23505 unique_violation, 23503 foreign_key_violation, 23514 check_violation
        names = {"23502": "NOT NULL", "23505": "UNIQUE/PK", "23503": "FOREIGN KEY", "23514": "CHECK"}
        got = names.get(code, code)
        if want in got or want == code:
            print(f"ok   REJECTED by {got} (as intended): {label}")
            return True
        print(f"FAIL rejected but by wrong rule ({got}, wanted {want}): {label}")
        return False


def seed(conn):
    with conn.cursor() as cur:
        cur.execute("INSERT INTO workflow(workflow_id,repo_url,commit_sha,language,ir_version) VALUES('wf1','u','abc123','python','1.0.0')")
        cur.execute("INSERT INTO variant(variant_id,workflow_id,label) VALUES('v3','wf1','v3')")
        cur.execute("INSERT INTO config(config_hash,variant_id,workflow_id,ir_version,lineage_json) VALUES(%s,'v3','wf1','1.0.0','{}')", (GOOD_HASH,))
        cur.execute("INSERT INTO node(workflow_id,node_id) VALUES('wf1','n1')")
        cur.execute("INSERT INTO eval_case(case_id,workflow_id) VALUES('c1','wf1')")
        cur.execute("INSERT INTO blob(content_hash,size_bytes) VALUES(%s,10)", (GOOD_HASH2,))
    conn.commit()


ER_COLS = "config_hash,variant_id,run_id,node_id,case_id,seed,ts,workflow_id,metric_name,value,unit"
def er_values(**over):
    base = dict(config_hash=GOOD_HASH, variant_id="v3", run_id="r1", node_id="n1", case_id="c1",
                seed=0, ts="2026-07-15T00:00:00Z", workflow_id="wf1",
                metric_name="llm.latency.total_ms", value=1.0, unit="ms")
    base.update(over)
    return base


def insert_er(over, cols=ER_COLS):
    vals = er_values(**over)
    collist = cols.split(",")
    placeholders = ",".join(["%s"] * len(collist))
    sql = f"INSERT INTO eval_result({cols}) VALUES({placeholders})"
    return sql, tuple(vals[c] for c in collist)


def main():
    conn = psycopg.connect()  # host/user/dbname from PG* env
    ok = True

    print("== apply up migration ==")
    exec_script(conn, UP)
    with conn.cursor() as cur:
        cur.execute("SELECT count(*) FROM information_schema.tables WHERE table_name IN "
                    "('workflow','variant','config','node','eval_case','blob','eval_result')")
        n = cur.fetchone()[0]
    print(f"ok   {n}/7 P0 tables created")
    ok &= (n == 7)

    print("== seed valid reference rows ==")
    seed(conn)

    print("== positive control: a fully-tagged eval_result inserts ==")
    sql, p = insert_er({})
    ok &= expect_ok(conn, "valid 7-tag eval_result (seed=0)", sql, p)

    print("== negative: each missing tag is rejected by NOT NULL ==")
    for tag in ["config_hash", "variant_id", "run_id", "node_id", "case_id", "seed", "ts"]:
        cols = ",".join(c for c in ER_COLS.split(",") if c != tag)  # omit the column entirely
        sql, p = insert_er({"run_id": "r_" + tag}, cols=cols)       # vary run_id to dodge the UNIQUE key
        ok &= expect_reject(conn, f"eval_result missing {tag}", "NOT NULL", sql, p)

    print("== negative: config_hash PK uniqueness (a config row is unique) ==")
    ok &= expect_reject(conn, "duplicate config_hash", "UNIQUE/PK",
                        "INSERT INTO config(config_hash,variant_id,workflow_id,ir_version,lineage_json) "
                        "VALUES(%s,'v3','wf1','1.0.0','{}')", (GOOD_HASH,))

    print("== negative: FK eval_result -> variant (dangling) ==")
    sql, p = insert_er({"variant_id": "nope", "run_id": "r_fk_variant"})
    ok &= expect_reject(conn, "eval_result.variant_id dangling", "FOREIGN KEY", sql, p)

    print("== negative: FK eval_result -> (workflow_id,node_id) node (dangling) ==")
    sql, p = insert_er({"node_id": "nope", "run_id": "r_fk_node"})
    ok &= expect_reject(conn, "eval_result.node_id dangling", "FOREIGN KEY", sql, p)

    print("== negative: FK eval_result -> eval_case (dangling) ==")
    sql, p = insert_er({"case_id": "nope", "run_id": "r_fk_case"})
    ok &= expect_reject(conn, "eval_result.case_id dangling", "FOREIGN KEY", sql, p)

    print("== negative: natural-key UNIQUE blocks a duplicate metric row (idempotency) ==")
    sql, p = insert_er({})  # identical to the positive-control row
    ok &= expect_reject(conn, "duplicate (config,run,node,case,seed,metric)", "UNIQUE/PK", sql, p)

    print("== negative: CHECK config_hash must be 64-hex ==")
    ok &= expect_reject(conn, "config_hash not hex", "CHECK",
                        "INSERT INTO config(config_hash,variant_id,workflow_id,ir_version,lineage_json) "
                        "VALUES('NOTHEX','v3','wf1','1.0.0','{}')")

    print("== negative: CHECK seed >= 0 ==")
    sql, p = insert_er({"seed": -1, "run_id": "r_seed_neg"})
    ok &= expect_reject(conn, "seed = -1", "CHECK", sql, p)

    print("== apply down migration; assert tables gone ==")
    exec_script(conn, DOWN)
    with conn.cursor() as cur:
        cur.execute("SELECT count(*) FROM information_schema.tables WHERE table_name IN "
                    "('workflow','variant','config','node','eval_case','blob','eval_result')")
        n = cur.fetchone()[0]
    print(f"ok   {n}/7 P0 tables remain after down migration" if n == 0 else f"FAIL {n} tables survived down")
    ok &= (n == 0)

    conn.close()
    print("\n" + ("ALL CONSTRAINT PROOFS PASSED" if ok else "SOME PROOFS FAILED"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
