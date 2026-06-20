-- SQL example: tables, queries, functions, and triggers.

-- Create the users table.
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    role TEXT DEFAULT 'user',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create the posts table.
CREATE TABLE posts (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    status TEXT DEFAULT 'draft',
    published_at TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Create the comments table.
CREATE TABLE comments (
    id INTEGER PRIMARY KEY,
    post_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    body TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (post_id) REFERENCES posts(id),
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Create indexes for performance.
CREATE INDEX idx_posts_user_id ON posts(user_id);
CREATE INDEX idx_posts_status ON posts(status);
CREATE INDEX idx_comments_post_id ON comments(post_id);
CREATE INDEX idx_comments_user_id ON comments(user_id);

-- Create a function to update the updated_at timestamp.
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create a trigger on the users table.
CREATE TRIGGER set_users_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- Select all published posts with author and comment count.
SELECT
    p.id,
    p.title,
    u.name AS author,
    COUNT(c.id) AS comment_count,
    p.published_at
FROM posts p
JOIN users u ON p.user_id = u.id
LEFT JOIN comments c ON p.id = c.post_id
WHERE p.status = 'published'
GROUP BY p.id, p.title, u.name, p.published_at
ORDER BY p.published_at DESC;

-- Select users with their total post count.
SELECT
    u.name,
    u.email,
    COUNT(p.id) AS post_count,
    SUM(CASE WHEN p.status = 'published' THEN 1 ELSE 0 END) AS published_count
FROM users u
LEFT JOIN posts p ON u.id = p.user_id
GROUP BY u.id, u.name, u.email
HAVING COUNT(p.id) > 0;

-- Insert test data.
INSERT INTO users (name, email, role) VALUES
    ('Alice', 'alice@example.com', 'admin'),
    ('Bob', 'bob@example.com', 'user'),
    ('Charlie', 'charlie@example.com', 'user');

INSERT INTO posts (user_id, title, body, status, published_at) VALUES
    (1, 'Hello World', 'My first post', 'published', '2025-01-15 10:00:00'),
    (2, 'Getting Started', 'Learning SQL', 'draft', NULL),
    (1, 'Advanced SQL', 'Joins and subqueries', 'published', '2025-02-20 14:30:00');

INSERT INTO comments (post_id, user_id, body) VALUES
    (1, 2, 'Great post!'),
    (1, 3, 'Thanks for sharing!'),
    (3, 2, 'Very helpful.');

-- Update a post.
UPDATE posts SET status = 'published', published_at = CURRENT_TIMESTAMP
WHERE id = 1;

DELETE FROM posts WHERE status = 'draft' AND created_at < '2024-01-01';

-- Select all users.
SELECT * FROM users;
