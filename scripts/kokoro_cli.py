#!/usr/bin/env python3
# Kokoro TTS CLI 包装（D03）：把 kokoro-onnx 运行时暴露为
#   kokoro --text <text> --voice <voice> --output <wav> [--model <onnx>] [--lang <code>] [--speed <x>]
# 风格的命令行，供 CloudWisePod 的 KokoroProvider（os/exec）直接调用。
#
# 已在 kokoro-onnx 1.x + kokoro-v1.0.onnx + voices-v1.0.bin 上验证：
#   - 英文 af_heart（en-us）与中文 zf_xiaobei/zm_yunyang（cmn）均可合成 24kHz wav；
#   - 中文 G2P 走 espeak-ng 的 cmn（Mandarin）语音，专名/数字/中英混合样本通过。
#
# 权重解析顺序（--model/--voices 显式参数 > 环境变量 > 默认目录）：
#   KOKORO_MODEL       完整 onnx 路径；voices 取同目录 voices-v1.0.bin
#   KOKORO_MODEL_DIR   目录，取其中 kokoro-v1.0.onnx + voices-v1.0.bin
#   默认               ~/.local/share/cloudwisepod/kokoro/
#
# 语言推断：未显式 --lang 时按 Kokoro v1.0 音色前缀选择 espeak 语言
#   zf_/zm_→cmn（中文）、af_/am_→en-us、bf_/bm_→en-gb、其余前缀见 LANG_BY_PREFIX。
import argparse
import os
import sys
from pathlib import Path

DEFAULT_MODEL_DIR = Path.home() / ".local/share/cloudwisepod/kokoro"

# 音色前缀 → espeak-ng 语言代码（Kokoro v1.0 官方音色命名，VOICE.md）。
LANG_BY_PREFIX = {
    "af": "en-us", "am": "en-us", "bf": "en-gb", "bm": "en-gb",
    "zf": "cmn", "zm": "cmn", "ef": "es", "ff": "fr-fr", "hf": "hi",
    "if": "it", "jf": "ja", "jm": "ja", "pf": "pt-br", "pm": "pt-br",
}
# --lang 接受的别名：配置里写 zh 即映射到 espeak 的普通话语音。
LANG_ALIASES = {"zh": "cmn", "zh-cn": "cmn", "mandarin": "cmn"}


def resolve_model(args):
    if args.model:
        model = Path(args.model)
    elif os.environ.get("KOKORO_MODEL"):
        model = Path(os.environ["KOKORO_MODEL"])
    else:
        model = Path(os.environ.get("KOKORO_MODEL_DIR", DEFAULT_MODEL_DIR)) / "kokoro-v1.0.onnx"
    if not model.is_file():
        sys.exit(f"kokoro: 模型文件不存在: {model}（可用 --model 或 KOKORO_MODEL/KOKORO_MODEL_DIR 指定）")
    voices = Path(args.voices) if args.voices else model.with_name("voices-v1.0.bin")
    if not voices.is_file():
        sys.exit(f"kokoro: 音色文件不存在: {voices}（应与模型同目录）")
    return model, voices


def resolve_lang(voice, lang_arg):
    if lang_arg:
        return LANG_ALIASES.get(lang_arg.lower(), lang_arg)
    prefix = voice.split("_", 1)[0].lower()
    return LANG_BY_PREFIX.get(prefix, "en-us")


def main():
    p = argparse.ArgumentParser(prog="kokoro", add_help=True)
    p.add_argument("--text", required=True, help="待合成文本")
    p.add_argument("--voice", default="af_heart", help="音色（默认 af_heart）")
    p.add_argument("--output", required=True, help="输出 wav 路径")
    p.add_argument("--model", default="", help="onnx 模型路径")
    p.add_argument("--voices", default="", help="voices bin 路径")
    p.add_argument("--lang", default="", help="语言代码（默认按音色前缀推断，zh=cmn）")
    p.add_argument("--speed", type=float, default=1.0, help="语速（默认 1.0）")
    args = p.parse_args()

    model, voices = resolve_model(args)
    lang = resolve_lang(args.voice, args.lang)

    # 延迟导入：参数错误时不必加载 onnxruntime（约 1s 启动开销）。
    from kokoro_onnx import Kokoro
    import soundfile as sf

    kokoro = Kokoro(str(model), str(voices))
    samples, sample_rate = kokoro.create(
        args.text, voice=args.voice, speed=args.speed, lang=lang,
    )
    sf.write(args.output, samples, sample_rate)


if __name__ == "__main__":
    main()
