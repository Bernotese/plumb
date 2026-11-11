-- 0006_no_image_binding (D-188, T-055): Plumb keeps no binding from an
-- Image to its template. The driver finds or makes the template at every
-- guest create, so the old rows are a stale cache. Target rows stay.
DELETE FROM binding WHERE kind = 'Image';
