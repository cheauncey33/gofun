package controller

import (
	"gofun/pkg/response"
	"gofun/service"
	"io"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

type UploadController struct {
	store *service.UploadStore
}

func NewUploadController(store *service.UploadStore) *UploadController {
	return &UploadController{store: store}
}

func (ctrl *UploadController) Create(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "请选择要上传的图片")
		return
	}
	src, err := file.Open()
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "读取上传文件失败")
		return
	}
	defer src.Close()
	uploaded, err := ctrl.store.SaveImage(src, file.Size)
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	response.Success(c, uploaded)
}

func (ctrl *UploadController) Get(c *gin.Context) {
	file, name, err := ctrl.store.Open(c.Param("name"))
	if err != nil {
		writeTicketCatalogError(c, err)
		return
	}
	defer file.Close()
	switch filepath.Ext(name) {
	case ".png":
		c.Header("Content-Type", "image/png")
	case ".webp":
		c.Header("Content-Type", "image/webp")
	case ".gif":
		c.Header("Content-Type", "image/gif")
	default:
		c.Header("Content-Type", "image/jpeg")
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, file)
}
