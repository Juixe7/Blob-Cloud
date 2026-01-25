CREATE TABLE user_file_views (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    viewed_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (user_id, file_id)
);

CREATE INDEX idx_user_file_views_user_viewed_at ON user_file_views(user_id, viewed_at DESC);
