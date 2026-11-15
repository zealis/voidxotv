// Command backfill-webp 给历史图集补生成展示用 WebP。
// 只写 display 对象与记录，不动原图；已经有的跳过。默认直接执行，-dry-run 只统计。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yejie/voidxotv/internal/platform/config"
	"github.com/yejie/voidxotv/internal/platform/database"
	"github.com/yejie/voidxotv/internal/platform/imageproc"
	"github.com/yejie/voidxotv/internal/platform/objectstore"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计，不写对象与数据库")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	store, err := objectstore.New(cfg)
	if err != nil {
		log.Fatalf("对象存储不可用: %v", err)
	}

	rows, err := db.Query(ctx, `
		SELECT m.id, s.object_key
		FROM media_assets m
		JOIN storage_objects s ON s.media_asset_id = m.id AND s.role = 'original'
		WHERE m.kind = 'image'
		  AND s.driver = 's3'
		  AND NOT EXISTS (
		    SELECT 1 FROM storage_objects d
		    WHERE d.media_asset_id = m.id AND d.role = 'display'
		  )
		ORDER BY m.id`)
	if err != nil {
		log.Fatal(err)
	}
	type job struct {
		id  int64
		key string
	}
	var jobs []job
	for rows.Next() {
		var item job
		if err := rows.Scan(&item.id, &item.key); err != nil {
			rows.Close()
			log.Fatal(err)
		}
		jobs = append(jobs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Fatal(err)
	}
	rows.Close()
	fmt.Printf("待处理 %d 张图\n", len(jobs))

	done, skipped, failed := 0, 0, 0
	for _, item := range jobs {
		data, err := store.GetBytes(ctx, item.key, 9<<20)
		if err != nil {
			failed++
			fmt.Printf("读取失败 asset=%d key=%s: %v\n", item.id, item.key, err)
			continue
		}
		display, ok := imageproc.MakeDisplay(data)
		if !ok {
			skipped++
			fmt.Printf("跳过（无法解码）asset=%d key=%s\n", item.id, item.key)
			continue
		}
		displayKey := strings.TrimSuffix(item.key, filepath.Ext(item.key)) + "-display.webp"
		if *dryRun {
			fmt.Printf("将写入 %s（%d 字节，原图 %d 字节）\n", displayKey, len(display), len(data))
			done++
			continue
		}
		if err := store.PutBytes(ctx, displayKey, display, "image/webp",
			"public, max-age=31536000, immutable"); err != nil {
			failed++
			fmt.Printf("写入失败 asset=%d: %v\n", item.id, err)
			continue
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO storage_objects (media_asset_id, driver, bucket, object_key, byte_size, role)
			VALUES ($1, 's3', $2, $3, $4, 'display')`,
			item.id, cfg.S3Bucket, displayKey, len(display)); err != nil {
			_ = store.Remove(ctx, displayKey)
			failed++
			fmt.Printf("登记失败 asset=%d: %v\n", item.id, err)
			continue
		}
		done++
	}
	fmt.Printf("完成 %d，跳过 %d，失败 %d\n", done, skipped, failed)
}
