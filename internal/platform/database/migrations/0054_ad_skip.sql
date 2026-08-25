ALTER TABLE users
    ADD COLUMN ad_skip_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE ad_fingerprints (
    fingerprint bytea PRIMARY KEY,
    confirm_count integer NOT NULL DEFAULT 0 CHECK (confirm_count >= 0),
    reject_count integer NOT NULL DEFAULT 0 CHECK (reject_count >= 0),
    CHECK (octet_length(fingerprint) = 32)
);
