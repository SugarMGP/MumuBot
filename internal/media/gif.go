package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/kolesa-team/go-webp/encoder"
)

const (
	maxGIFFrames = 256
	maxGIFPixels = 64 * 1024 * 1024
	gifCellSide  = 512
)

// ImagePreview 只转换交给模型的 GIF 预览，发送和收藏保留原件
func ImagePreview(data []byte, mime string) ([]byte, string, string, error) {
	if mime != "image/gif" {
		return data, mime, "", nil
	}
	count, width, height, limited, err := inspectGIF(data)
	if err != nil {
		return nil, "", "", err
	}
	var animation *gif.GIF
	if limited {
		first, decodeErr := gif.Decode(bytes.NewReader(data))
		if decodeErr != nil {
			return nil, "", "", decodeErr
		}
		animation = &gif.GIF{Image: []*image.Paletted{first.(*image.Paletted)}, Config: image.Config{Width: width, Height: height}}
	} else {
		animation, err = gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, "", "", fmt.Errorf("GIF 解码失败: %w", err)
		}
	}
	sheet, positions := gifContactSheet(animation)
	options, err := encoder.NewLossyEncoderOptions(encoder.PresetDefault, 85)
	if err != nil {
		return nil, "", "", err
	}
	enc, err := encoder.NewEncoder(sheet, options)
	if err != nil {
		return nil, "", "", err
	}
	var output bytes.Buffer
	if err := enc.Encode(&output); err != nil {
		return nil, "", "", err
	}
	note := fmt.Sprintf("这是 GIF 的抽帧预览，共 %d 帧，按从左到右、从上到下排列；位置：%s。只看到了这些画面，不能推断未采样的细节。", count, strings.Join(positions, "、"))
	if limited {
		note = fmt.Sprintf("GIF 共 %d 帧，超过解码帧数或像素上限，本次仅提供首帧，不能据此判断完整动画。", count)
	}
	return output.Bytes(), webpMime, note, nil
}

// inspectGIF 在 DecodeAll 分配内存前检查容器、帧数和画布预算
func inspectGIF(data []byte) (count, width, height int, limited bool, err error) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, 0, 0, false, fmt.Errorf("GIF 文件头无效")
	}
	width = int(binary.LittleEndian.Uint16(data[6:8]))
	height = int(binary.LittleEndian.Uint16(data[8:10]))
	if width == 0 || height == 0 || int64(width)*int64(height) > maxGIFPixels {
		return 0, 0, 0, false, fmt.Errorf("GIF 首帧画布超过像素上限或为空")
	}
	pos := 13
	if data[10]&128 != 0 {
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for pos < len(data) {
			size := int(data[pos])
			pos++
			if size == 0 {
				return true
			}
			pos += size
			if pos > len(data) {
				return false
			}
		}
		return false
	}
	for pos < len(data) {
		marker := data[pos]
		pos++
		switch marker {
		case 0x3b:
			if count == 0 {
				return 0, 0, 0, false, fmt.Errorf("GIF 没有画面")
			}
			return count, width, height, count > maxGIFFrames || int64(count)*int64(width)*int64(height) > maxGIFPixels, nil
		case 0x21:
			if pos >= len(data) {
				break
			}
			pos++
			if !skipBlocks() {
				return 0, 0, 0, false, fmt.Errorf("GIF 扩展数据不完整")
			}
		case 0x2c:
			if len(data)-pos < 9 {
				return 0, 0, 0, false, fmt.Errorf("GIF 帧头不完整")
			}
			left := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
			top := int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
			w := int(binary.LittleEndian.Uint16(data[pos+4 : pos+6]))
			h := int(binary.LittleEndian.Uint16(data[pos+6 : pos+8]))
			packed := data[pos+8]
			pos += 9
			if w == 0 || h == 0 || left+w > width || top+h > height {
				return 0, 0, 0, false, fmt.Errorf("GIF 帧超出画布或为空")
			}
			if packed&128 != 0 {
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			if pos >= len(data) {
				return 0, 0, 0, false, fmt.Errorf("GIF 图像数据不完整")
			}
			pos++
			if !skipBlocks() {
				return 0, 0, 0, false, fmt.Errorf("GIF 图像数据不完整")
			}
			count++
		default:
			return 0, 0, 0, false, fmt.Errorf("GIF 容器无效")
		}
	}
	return 0, 0, 0, false, fmt.Errorf("GIF 缺少结束标记")
}

func gifContactSheet(animation *gif.GIF) (image.Image, []string) {
	count := len(animation.Image)
	selected := min(count, 9)
	indices := make(map[int]bool, selected)
	for i := 0; i < selected; i++ {
		index := 0
		if selected > 1 {
			index = i * (count - 1) / (selected - 1)
		}
		indices[index] = true
	}
	width, height := animation.Config.Width, animation.Config.Height
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	background := color.Color(color.Transparent)
	if palette, ok := animation.Config.ColorModel.(color.Palette); ok && int(animation.BackgroundIndex) < len(palette) {
		background = palette[animation.BackgroundIndex]
		if int(animation.BackgroundIndex) < len(animation.Image[0].Palette) {
			_, _, _, alpha := animation.Image[0].Palette[animation.BackgroundIndex].RGBA()
			if alpha == 0 {
				background = color.Transparent
			}
		}
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	cellWidth, cellHeight := width, height
	if max(width, height) > gifCellSide {
		scale := float64(gifCellSide) / float64(max(width, height))
		cellWidth = max(1, int(float64(width)*scale))
		cellHeight = max(1, int(float64(height)*scale))
	}
	cols := int(math.Ceil(math.Sqrt(float64(selected))))
	rows := (selected + cols - 1) / cols
	sheet := image.NewRGBA(image.Rect(0, 0, cols*cellWidth, rows*cellHeight))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	positions := make([]string, 0, selected)
	elapsed := 0
	for index, frame := range animation.Image {
		var before *image.RGBA
		disposal := byte(0)
		if index < len(animation.Disposal) {
			disposal = animation.Disposal[index]
		}
		if disposal == gif.DisposalPrevious {
			before = image.NewRGBA(canvas.Bounds())
			copy(before.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if indices[index] {
			preview := imaging.Resize(canvas, cellWidth, cellHeight, imaging.Lanczos)
			cell := len(positions)
			point := image.Pt(cell%cols*cellWidth, cell/cols*cellHeight)
			draw.Draw(sheet, image.Rectangle{Min: point, Max: point.Add(preview.Bounds().Size())}, preview, preview.Bounds().Min, draw.Over)
			positions = append(positions, fmt.Sprintf("第%d帧(%d毫秒)", index+1, elapsed))
		}
		if index < len(animation.Delay) {
			elapsed += animation.Delay[index] * 10
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = before
		}
	}
	return sheet, positions
}
