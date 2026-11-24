// A synchronous, non-blocking decoder. ArkTS retains compressed samples until the codec has room.
#include <napi/native_api.h>
#include <multimedia/player_framework/native_avbuffer.h>
#include <multimedia/player_framework/native_avcapability.h>
#include <multimedia/player_framework/native_avcodec_videodecoder.h>
#include <multimedia/player_framework/native_avformat.h>
#include <native_window/external_window.h>
#include <charconv>
#include <cstring>
#include <memory>
#include <string>

namespace {
struct Decoder {
    OH_AVCodec* codec = nullptr;
    OHNativeWindow* window = nullptr;
    void Close() {
        if (codec) OH_VideoDecoder_Destroy(codec);
        if (window) OH_NativeWindow_DestroyNativeWindow(window);
        codec = nullptr;
        window = nullptr;
    }
    ~Decoder() { Close(); }
};

napi_value Undefined(napi_env env) {
    napi_value value;
    napi_get_undefined(env, &value);
    return value;
}
napi_value Number(napi_env env, int32_t n) {
    napi_value value;
    napi_create_int32(env, n, &value);
    return value;
}
std::string String(napi_env env, napi_value value) {
    char buffer[48];
    size_t len = 0;
    if (napi_get_value_string_utf8(env, value, buffer, sizeof(buffer), &len) != napi_ok || len >= sizeof(buffer) - 1) return {};
    return std::string(buffer, len);
}
const char* HardwareDecoder(const std::string& codec) {
    const char* mime = codec == "hevc" ? "video/hevc" : codec == "h264" ? "video/avc" : nullptr;
    if (!mime) return nullptr;
    auto* cap = OH_AVCodec_GetCapabilityByCategory(mime, false, HARDWARE);
    return cap ? OH_AVCapability_GetName(cap) : nullptr;
}
napi_value Supports(napi_env env, napi_callback_info info) {
    napi_value arg, result;
    size_t n = 1;
    napi_get_cb_info(env, info, &n, &arg, nullptr, nullptr);
    napi_get_boolean(env, n == 1 && HardwareDecoder(String(env, arg)) != nullptr, &result);
    return result;
}
napi_value Create(napi_env env, napi_callback_info info) {
    size_t n = 4;
    napi_value args[4];
    napi_get_cb_info(env, info, &n, args, nullptr, nullptr);
    if (n != 4) return Undefined(env);
    const auto codec = String(env, args[0]), surface = String(env, args[1]);
    int32_t w = 0, h = 0;
    uint64_t id = 0;
    auto parsed = std::from_chars(surface.data(), surface.data() + surface.size(), id);
    const char* name = HardwareDecoder(codec);
    if (!name || !id || parsed.ec != std::errc{} || parsed.ptr != surface.data() + surface.size() ||
        napi_get_value_int32(env, args[2], &w) != napi_ok || napi_get_value_int32(env, args[3], &h) != napi_ok ||
        w < 1 || h < 1 || w > 4096 || h > 4096 || int64_t(w) * h > 9 * 1024 * 1024) return Undefined(env);
    auto d = std::make_unique<Decoder>();
    if (OH_NativeWindow_CreateNativeWindowFromSurfaceId(id, &d->window) != 0) return Undefined(env);
    d->codec = OH_VideoDecoder_CreateByName(name);
    if (!d->codec) return Undefined(env);
    auto* format = OH_AVFormat_CreateVideoFormat(codec == "hevc" ? "video/hevc" : "video/avc", w, h);
    if (!format) return Undefined(env);
    OH_AVFormat_SetIntValue(format, OH_MD_KEY_ENABLE_SYNC_MODE, 1);
    OH_AVFormat_SetIntValue(format, OH_MD_KEY_VIDEO_ENABLE_LOW_LATENCY, 1);
    auto rc = OH_VideoDecoder_Configure(d->codec, format);
    OH_AVFormat_Destroy(format);
    if (rc != AV_ERR_OK || OH_VideoDecoder_SetSurface(d->codec, d->window) != AV_ERR_OK ||
        OH_VideoDecoder_Prepare(d->codec) != AV_ERR_OK || OH_VideoDecoder_Start(d->codec) != AV_ERR_OK) return Undefined(env);
    napi_value result;
    if (napi_create_external(env, d.get(), [](napi_env, void* data, void*) { delete static_cast<Decoder*>(data); },
                             nullptr, &result) != napi_ok) return Undefined(env);
    d.release();
    return result;
}
Decoder* Handle(napi_env env, napi_value value) {
    void* data = nullptr;
    return napi_get_value_external(env, value, &data) == napi_ok ? static_cast<Decoder*>(data) : nullptr;
}
napi_value Submit(napi_env env, napi_callback_info info) {
    napi_value args[4];
    size_t n = 4, length = 0;
    napi_get_cb_info(env, info, &n, args, nullptr, nullptr);
    auto* d = n == 4 ? Handle(env, args[0]) : nullptr;
    void* bytes = nullptr;
    int64_t pts = 0;
    uint32_t flags = 0, index = 0;
    if (!d || !d->codec || napi_get_arraybuffer_info(env, args[1], &bytes, &length) != napi_ok ||
        !bytes || !length || length > 4 * 1024 * 1024 || napi_get_value_int64(env, args[2], &pts) != napi_ok ||
        napi_get_value_uint32(env, args[3], &flags) != napi_ok) return Number(env, -1);
    auto rc = OH_VideoDecoder_QueryInputBuffer(d->codec, &index, 0);
    if (rc == AV_ERR_TRY_AGAIN_LATER) return Number(env, 0);
    if (rc != AV_ERR_OK) return Number(env, -1);
    auto* buffer = OH_VideoDecoder_GetInputBuffer(d->codec, index);
    if (!buffer || OH_AVBuffer_GetCapacity(buffer) < static_cast<int32_t>(length)) return Number(env, -1);
    auto* dest = OH_AVBuffer_GetAddr(buffer);
    if (!dest) return Number(env, -1);
    std::memcpy(dest, bytes, length);
    OH_AVCodecBufferAttr attr{pts, static_cast<int32_t>(length), 0, flags};
    if (OH_AVBuffer_SetBufferAttr(buffer, &attr) != AV_ERR_OK) return Number(env, -1);
    return Number(env, OH_VideoDecoder_PushInputBuffer(d->codec, index) == AV_ERR_OK ? 1 : -1);
}
napi_value Render(napi_env env, napi_callback_info info) {
    size_t n = 1;
    napi_value arg;
    napi_get_cb_info(env, info, &n, &arg, nullptr, nullptr);
    auto* d = n == 1 ? Handle(env, arg) : nullptr;
    if (!d || !d->codec) return Number(env, -1);
    // Drain decoded pictures in order, then put just the newest one onto the surface.
    int consumed = 0;
    uint32_t latest = 0;
    for (int i = 0; i < 8; ++i) {
        uint32_t index = 0;
        auto rc = OH_VideoDecoder_QueryOutputBuffer(d->codec, &index, 0);
        if (rc == AV_ERR_STREAM_CHANGED) continue;
        if (rc == AV_ERR_TRY_AGAIN_LATER) break;
        if (rc != AV_ERR_OK) return Number(env, -1);
        if (consumed && OH_VideoDecoder_FreeOutputBuffer(d->codec, latest) != AV_ERR_OK) return Number(env, -1);
        latest = index;
        ++consumed;
    }
    if (consumed && OH_VideoDecoder_RenderOutputBuffer(d->codec, latest) != AV_ERR_OK) return Number(env, -1);
    return Number(env, consumed);
}
napi_value Close(napi_env env, napi_callback_info info) {
    size_t n = 1;
    napi_value arg;
    napi_get_cb_info(env, info, &n, &arg, nullptr, nullptr);
    auto* d = n == 1 ? Handle(env, arg) : nullptr;
    if (d) d->Close();
    return Undefined(env);
}
napi_value Init(napi_env env, napi_value exports) {
    napi_property_descriptor methods[] = {
        {"supports", nullptr, Supports}, {"create", nullptr, Create}, {"submit", nullptr, Submit},
        {"render", nullptr, Render}, {"close", nullptr, Close}
    };
    napi_define_properties(env, exports, sizeof(methods) / sizeof(methods[0]), methods);
    return exports;
}
napi_module module = {1, 0, nullptr, Init, "persona_screen", nullptr, {0}};
}
extern "C" __attribute__((constructor)) void RegisterPersonaScreen() { napi_module_register(&module); }
