-- 0010_plan_images (T-062, D-199): the image part of a Plan. A JSON array
-- of plan.ImagePlan: builds and template removals that the Run does next to
-- its targets. [] for every Plan without one, also every older Plan.
ALTER TABLE plan ADD COLUMN images TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(images) AND json_type(images) = 'array');
