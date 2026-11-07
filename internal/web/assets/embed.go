package assets

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var embeddedFiles embed.FS

func fileSystem() fs.FS {
	sub, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Open 读取构建产物中的单个文件，供 SPA 外壳与站点图标使用
func Open(name string) ([]byte, error) {
	return fs.ReadFile(fileSystem(), name)
}

func Handler() http.Handler {
	assets, err := fs.Sub(fileSystem(), "assets")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 该处理器只挂载在 /assets/ 下，Vite 产物文件名带内容哈希，可长期缓存
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}
