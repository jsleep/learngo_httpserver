-- +goose Up
ALTER TABLE users ADD COLUMN is_chirpy_red boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN is_chirpy_red;