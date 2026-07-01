-- 011_skills.sql — global Skills repository (SKILL.md: frontmatter + body).
-- Versions immutable; aliases mutable pointers (@latest auto-moves). Global scope.
CREATE TABLE IF NOT EXISTS skills (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,          -- slug from frontmatter.name
    description text NOT NULL DEFAULT '',       -- mirrors latest version
    tags        text[] NOT NULL DEFAULT '{}',
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_skills_tags ON skills USING GIN (tags);

CREATE TABLE IF NOT EXISTS skill_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id     uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    frontmatter  jsonb NOT NULL DEFAULT '{}'::jsonb,
    body         text NOT NULL,
    raw          text NOT NULL,                 -- assembled SKILL.md (frontmatter+body)
    description  text NOT NULL DEFAULT '',
    tags         text[] NOT NULL DEFAULT '{}',
    comment      text NOT NULL DEFAULT '',
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, version_no)
);
CREATE INDEX IF NOT EXISTS idx_skill_versions_skill ON skill_versions(skill_id, version_no DESC);

CREATE TABLE IF NOT EXISTS skill_aliases (
    skill_id   uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    alias      text NOT NULL,
    version_no int NOT NULL,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, alias),
    FOREIGN KEY (skill_id, version_no) REFERENCES skill_versions(skill_id, version_no) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_skill_aliases_skill ON skill_aliases(skill_id);
