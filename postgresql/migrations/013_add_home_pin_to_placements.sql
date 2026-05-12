-- Pin-to-home + home page reordering. Existing placements default to pinned
-- so the home page keeps showing every log on first deploy; users can unpin
-- individually. home_position is a separate ordering from the folder-tree
-- position so the home page and /logs page can be reordered independently.

ALTER TABLE user_log_placements
    ADD COLUMN pinned_to_home boolean NOT NULL DEFAULT true,
    ADD COLUMN home_position integer;

UPDATE user_log_placements p
SET home_position = sub.rn - 1
FROM (
    SELECT p.user_id, p.log_id,
           row_number() OVER (
               PARTITION BY p.user_id
               ORDER BY lower(l.name), p.log_id
           ) AS rn
    FROM user_log_placements p JOIN logs l ON l.id = p.log_id
) sub
WHERE p.user_id = sub.user_id AND p.log_id = sub.log_id;

ALTER TABLE user_log_placements ALTER COLUMN home_position SET NOT NULL;

CREATE INDEX user_log_placements_user_home_position_idx
    ON user_log_placements (user_id, home_position)
    WHERE pinned_to_home;

---- create above / drop below ----

DROP INDEX user_log_placements_user_home_position_idx;
ALTER TABLE user_log_placements
    DROP COLUMN home_position,
    DROP COLUMN pinned_to_home;
