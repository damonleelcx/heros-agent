# Operator/Admin console (P8) — showcased on its own origin (second origin, ADR/D5)

Served by `cmd/p8hermes` (admin API :4311, BFF-gated) + `web/admin-console` BFF (:4310).
Sign-in requires SSO + a verified MFA factor; acting principal `adm-superadmin` (SUPERADMIN).
Separate origin + disjoint cookie jar from the customer console (:4320) — verified live.
