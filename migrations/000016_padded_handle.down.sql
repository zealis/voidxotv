-- 回退到不带前导零的夜街号。昵称只回收默认形态，自定义昵称不受影响。
UPDATE users SET handle = 'pad-' || id
 WHERE handle IS DISTINCT FROM id::text;

UPDATE users SET handle = id::text
 WHERE handle = 'pad-' || id;

UPDATE users SET display_name = '用户' || id::text
 WHERE display_name = '用户' || lpad(id::text, 6, '0');
