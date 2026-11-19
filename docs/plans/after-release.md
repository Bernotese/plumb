# After the first release – backlog (not planned, not started)

Points that the release QA sweep (T-260, `docs/tasks/T-260.log.md`) found worth a card, and that `pl-lead` moved behind the first release. Nothing here blocks v0.1.0.

| From | Point | Why later |
| --- | --- | --- |
| #361 L2 (row 35) | `keyAttr` in the web is a hand copy of `Type.Key`: put the key attribute into `resourceKinds.json` and read it in the Block form and the mock | a refactor with no user-visible fault today; a test already catches a drift |
| #374 L2 (row 69) | a controller stop between the revoke and the restore call says "may have no disk" though no disk was touched: store a "restore call begins" mark | too careful, not wrong; a change in the restore path needs its own critical review and host proof |
| #382 (row 76, O9 b) | a re-enroll Plan goes stale with any head move: let it survive when the Target's rendered documents are equal at its base and at the head | a comfort only (the Issue offers the way out, D-440); it touches the staleness rule of every Plan |
| O9 (a) | after a cut restore Run the drop of older safety backups waits for the next restore that works | harmless (one more backup for a while); the README says it |
