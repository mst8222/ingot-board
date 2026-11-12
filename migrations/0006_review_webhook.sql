-- 待评审出站地址。空字符串表示不发。任务数据仍在原表。

CREATE TABLE review_webhook (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    url TEXT NOT NULL DEFAULT ''
);

INSERT INTO review_webhook(id, url) VALUES (1, '');
