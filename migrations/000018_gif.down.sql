DROP TABLE IF EXISTS gif_search_cache;
DELETE FROM system_settings WHERE key IN (
    'gif.provider', 'gif.apihz_line', 'gif.apihz_id',
    'gif.apihz_key', 'gif.apihz_limit', 'gif.cache_days'
);
