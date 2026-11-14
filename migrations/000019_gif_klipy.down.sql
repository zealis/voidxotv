DELETE FROM system_settings WHERE key IN (
    'gif.klipy_key', 'gif.klipy_filter', 'gif.klipy_locale',
    'gif.klipy_limit', 'gif.klipy_cache_days'
);
