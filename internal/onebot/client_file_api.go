package onebot

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"mumu-bot/internal/filemeta"

	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

type groupFileURLRequest struct {
	api.GetGroupFileURLRequest
	BusID *int64 `json:"busid,omitempty"`
}

type privateFileURLRequest struct {
	api.GetPrivateFileURLRequest
	FileHash *string `json:"file_hash,omitempty"`
}

// ResolveFileURL 用原消息定位信息换取当前下载地址；不解码 base64 或本地路径
func (c *Client) ResolveFileURL(ctx context.Context, kind string, targetID int64, ref filemeta.Ref) (string, error) {
	client, err := c.apiClient()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ref.FileID) == "" {
		return "", fmt.Errorf("文件缺少 file_id")
	}
	switch kind {
	case "group":
		req := api.GetGroupFileURLRequest{
			GroupID: message.ID(targetID), FileID: ref.FileID,
		}
		var resp *api.GetGroupFileURLResponse
		if ref.BusID == nil {
			resp, err = client.GetGroupFileURL(ctx, req)
		} else {
			// SDK 的生成请求尚未覆盖 busid，保留原消息给出的定位信息
			resp = &api.GetGroupFileURLResponse{}
			err = client.Call(ctx, string(api.ActionGetGroupFileURL), groupFileURLRequest{GetGroupFileURLRequest: req, BusID: ref.BusID}, resp)
		}
		if err != nil {
			return "", err
		}
		if resp == nil || resp.URL == nil || strings.TrimSpace(*resp.URL) == "" {
			return "", fmt.Errorf("后端没有返回群文件下载地址")
		}
		return *resp.URL, nil
	case "private":
		req := api.GetPrivateFileURLRequest{FileID: ref.FileID}
		var resp *api.GetPrivateFileURLResponse
		if ref.FileHash == "" {
			resp, err = client.GetPrivateFileURL(ctx, req)
		} else {
			// SDK 的生成请求尚未覆盖 file_hash，不能在首次取文件时丢弃
			resp = &api.GetPrivateFileURLResponse{}
			err = client.Call(ctx, string(api.ActionGetPrivateFileURL), privateFileURLRequest{GetPrivateFileURLRequest: req, FileHash: &ref.FileHash}, resp)
		}
		if err != nil {
			return "", err
		}
		if resp == nil || resp.URL == nil || strings.TrimSpace(*resp.URL) == "" {
			return "", fmt.Errorf("后端没有返回私聊文件下载地址")
		}
		return *resp.URL, nil
	default:
		return "", fmt.Errorf("文件会话类型无效")
	}
}

// UploadFileBytes 发送真实文件字节；回执只有 file_id，不伪造消息编号
func (c *Client) UploadFileBytes(ctx context.Context, kind string, targetID int64, name string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("文件为空")
	}
	client, err := c.apiClient()
	if err != nil {
		return "", err
	}
	source := "base64://" + base64.StdEncoding.EncodeToString(data)
	switch kind {
	case "group":
		resp, err := client.UploadGroupFile(ctx, api.UploadGroupFileRequest{
			GroupID: message.ID(targetID), File: source, Name: name, UploadFile: true,
		})
		if err != nil {
			return "", err
		}
		if resp == nil || resp.FileID == nil || strings.TrimSpace(*resp.FileID) == "" {
			return "", fmt.Errorf("上传回执没有 file_id")
		}
		return *resp.FileID, nil
	case "private":
		resp, err := client.UploadPrivateFile(ctx, api.UploadPrivateFileRequest{
			UserID: message.ID(targetID), File: source, Name: name, UploadFile: true,
		})
		if err != nil {
			return "", err
		}
		if resp == nil || resp.FileID == nil || strings.TrimSpace(*resp.FileID) == "" {
			return "", fmt.Errorf("上传回执没有 file_id")
		}
		return *resp.FileID, nil
	default:
		return "", fmt.Errorf("文件会话类型无效")
	}
}

// SendImageBytes 把真实图片字节作为图片消息发送
func (c *Client) SendImageBytes(ctx context.Context, kind string, targetID int64, data []byte, sticker bool) (int64, uint64, error) {
	if len(data) == 0 {
		return 0, 0, fmt.Errorf("图片为空")
	}
	subType := "0"
	if sticker {
		subType = "1"
	}
	segment := message.Segment{Type: "image", Data: message.ImageData{
		File: "base64://" + base64.StdEncoding.EncodeToString(data), SubType: message.StrNum(subType),
	}}
	return c.sendChain(ctx, kind, targetID, message.ChainOf(segment))
}
