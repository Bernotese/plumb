-- 0013_step_rolled_back (T-116; D-293, D-300).
--
-- * run_step.rolled_back: a safe apply changed the resource, got no round
--   trip to the controller within the window and put the confirmed state
--   back. Only on a failed step (RunStep.rolled_back in the API).

ALTER TABLE run_step ADD COLUMN rolled_back INTEGER NOT NULL DEFAULT 0 CHECK (rolled_back IN (0, 1));
