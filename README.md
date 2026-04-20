# CapCut Export Tool

> 🇬🇧 [English](#english) · 🇻🇳 [Tiếng Việt](#tiếng-việt)

---

<a id="english"></a>
## 🇬🇧 English

A native Windows desktop GUI app (built with Go + Gio UI) that batch-exports CapCut draft segments into numbered MP4 clips — with real-time per-worker progress and GPU acceleration.

### ✨ Features

- 🎬 **Batch export** — reads `draft_content.json` once and exports every segment in parallel
- ⚡ **GPU-accelerated** — auto-detects NVIDIA GPU and uses `h264_nvenc`; falls back to `libx264` (CPU)
- 📊 **Live progress** — per-worker progress bars with `%`, `folder/file.mp4` label, job counter (`3/12`), and GPU/CPU badge
- 🗂️ **Config persistence** — remembers your last project dir and output dir
- 🔒 **Export-safe** — reads the draft only once at start; you can keep editing in CapCut while it exports
- 🖥️ **No console flashes** — all subprocess windows are hidden

### 📋 Requirements

| Tool | Notes |
|------|-------|
| [FFmpeg](https://ffmpeg.org/download.html) | Must be in `PATH` |
| [FFprobe](https://ffmpeg.org/download.html) | Bundled with FFmpeg |
| NVIDIA GPU *(optional)* | For hardware-accelerated encoding |

### 🚀 Usage

#### Option A — Pre-built binary (recommended)

1. Go to the [**Releases**](../../releases/latest) page
2. Download `capcut-export.exe`
3. Run it — no installation needed
4. Click **Browse** to select your CapCut project folder (the folder containing `draft_content.json`)
5. Click **Browse** to select an output folder
6. Click **Start Export**

#### Option B — Build from source

```bash
# Requires Go 1.21+
go build -ldflags "-H=windowsgui" -o capcut-export.exe .
```

### 📁 Project Structure

```
capcut-project-exporter/
├── main.go        # Gio UI window & layout
├── export.go      # Export logic (draft parsing, FFmpeg)
├── config.go      # Config persistence
├── go.mod
└── README.md
```

### 📝 How it Works

1. Reads `draft_content.json` from the selected CapCut project folder
2. Resolves each timeline segment (simple clips and compound/nested clips)
3. Builds FFmpeg filter graphs to trim, scale, and encode each segment
4. Runs N workers in parallel (configurable, default 4)
5. Outputs numbered MP4 files: `001_ClipName.mp4`, `002_ClipName.mp4`, …

---

<a id="tiếng-việt"></a>
## 🇻🇳 Tiếng Việt

Ứng dụng desktop Windows (Go + Gio UI) giúp export hàng loạt các đoạn clip trong dự án CapCut ra file MP4 có đánh số — với tiến trình thời gian thực và tăng tốc GPU.

### ✨ Tính năng

- 🎬 **Export hàng loạt** — đọc `draft_content.json` một lần rồi export song song tất cả segment
- ⚡ **Tăng tốc GPU** — tự phát hiện NVIDIA GPU, dùng `h264_nvenc`; tự động fallback về `libx264` (CPU)
- 📊 **Tiến trình trực tiếp** — progress bar từng worker với `%`, nhãn `folder/file.mp4`, đếm job (`3/12`), badge GPU/CPU
- 🗂️ **Lưu config** — nhớ thư mục project và thư mục output lần dùng cuối
- 🔒 **An toàn khi export** — đọc draft một lần lúc bắt đầu; bạn có thể tiếp tục chỉnh sửa trong CapCut trong lúc export
- 🖥️ **Không flash console** — ẩn hết cửa sổ subprocess

### 📋 Yêu cầu

| Công cụ | Ghi chú |
|---------|---------|
| [FFmpeg](https://ffmpeg.org/download.html) | Phải có trong `PATH` |
| [FFprobe](https://ffmpeg.org/download.html) | Đi kèm FFmpeg |
| NVIDIA GPU *(tuỳ chọn)* | Để encode bằng phần cứng |

### 🚀 Hướng dẫn sử dụng

#### Cách A — Tải file exe có sẵn (khuyến nghị)

1. Vào trang [**Releases**](../../releases/latest)
2. Tải file `capcut-export.exe`
3. Chạy thẳng — không cần cài đặt
4. Bấm **Browse** để chọn thư mục dự án CapCut (thư mục chứa `draft_content.json`)
5. Bấm **Browse** để chọn thư mục output
6. Bấm **Start Export**

> 💡 **Có thể làm việc song song:** Sau khi bấm Start Export, bạn hoàn toàn có thể tiếp tục chỉnh sửa dự án trong CapCut — app đã đọc xong `draft_content.json` và không bị ảnh hưởng bởi thay đổi sau đó.

#### Cách B — Tự build từ source

```bash
# Cần Go 1.21+
go build -ldflags "-H=windowsgui" -o capcut-export.exe .
```

### 📁 Cấu trúc thư mục

```
capcut-project-exporter/
├── main.go        # Cửa sổ Gio UI & layout
├── export.go      # Logic export (đọc draft, FFmpeg)
├── config.go      # Lưu/đọc config
├── go.mod
└── README.md
```

### 📝 Cách hoạt động

1. Đọc `draft_content.json` từ thư mục dự án CapCut
2. Phân giải từng segment trên timeline (clip đơn và compound/nested clip)
3. Xây dựng FFmpeg filter graph để trim, scale và encode từng segment
4. Chạy N worker song song (có thể tuỳ chỉnh, mặc định 4)
5. Xuất file MP4 đánh số: `001_TênClip.mp4`, `002_TênClip.mp4`, …

---

## ☕ Support / Ủng hộ

Nếu tool này giúp ích cho bạn, hãy mời mình một ly cà phê nhé! 😊  
*If this tool helps you, consider buying me a coffee!*

<table>
  <tr>
    <td align="center">
      <b>☕ Buy Me a Coffee</b><br><br>
      <a href="https://buymeacoffee.com/gyftd43bs">
        <img src="https://api.qrserver.com/v1/create-qr-code/?size=160x160&data=https://buymeacoffee.com/gyftd43bs" alt="Buy Me a Coffee QR" width="160" height="160"/><br>
        <img src="https://img.shields.io/badge/Buy%20Me%20a%20Coffee-gyftd43bs-FFDD00?style=for-the-badge&logo=buy-me-a-coffee&logoColor=black" alt="Buy Me a Coffee"/>
      </a>
    </td>
    <td align="center">
      <b>💳 PayPal</b><br><br>
      <a href="https://paypal.me/daovanhungblogger">
        <img src="https://api.qrserver.com/v1/create-qr-code/?size=160x160&data=https://paypal.me/daovanhungblogger" alt="PayPal QR" width="160" height="160"/><br>
        <img src="https://img.shields.io/badge/PayPal-daovanhungblogger-00457C?style=for-the-badge&logo=paypal&logoColor=white" alt="PayPal"/>
      </a>
    </td>
  </tr>
</table>

---

## 📄 License

MIT

