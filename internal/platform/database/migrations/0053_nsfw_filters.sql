CREATE TABLE nsfw_filters (
    id bigserial PRIMARY KEY,
    keyword text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);
