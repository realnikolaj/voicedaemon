#include "apm_wrapper.h"

#include <modules/audio_processing/include/audio_processing.h>
#include <cstring>

// Wrapper struct holding the ref-counted APM instance.
struct APMHandle {
    rtc::scoped_refptr<webrtc::AudioProcessing> apm;
};

// Internal helper: create APM with configurable AEC.
static void* apm_create_internal(bool enable_aec) {
    webrtc::AudioProcessing::Config config;

    config.noise_suppression.enabled = true;
    config.noise_suppression.level =
        webrtc::AudioProcessing::Config::NoiseSuppression::kHigh;

    config.high_pass_filter.enabled = true;

    config.gain_controller2.enabled = true;
    config.gain_controller2.adaptive_digital.enabled = true;

    config.echo_canceller.enabled = enable_aec;

    auto apm = webrtc::AudioProcessingBuilder()
        .SetConfig(config)
        .Create();

    if (!apm) {
        return nullptr;
    }

    auto* h = new APMHandle();
    h->apm = apm;
    return static_cast<void*>(h);
}

extern "C" {

void* apm_create(void) {
    return apm_create_internal(false);
}

void* apm_create_with_aec(void) {
    return apm_create_internal(true);
}

int apm_process_capture(void* handle, float* samples, int num_samples) {
    if (!handle || !samples || num_samples != 480) {
        return -1;
    }

    auto* h = static_cast<APMHandle*>(handle);

    // Set up single-channel float pointer for deinterleaved API.
    float* channel_ptr = samples;

    webrtc::StreamConfig stream_config(48000, 1);

    int err = h->apm->ProcessStream(
        &channel_ptr,       // src: pointer to array of channel pointers
        stream_config,      // input config
        stream_config,      // output config
        &channel_ptr        // dest: same buffer (in-place)
    );

    if (err != 0) {
        return -1;
    }

    // Check voice detection from statistics.
    // Note: voice_detected is deprecated in v2 and may be empty.
    auto stats = h->apm->GetStatistics();
    if (stats.voice_detected.has_value()) {
        return stats.voice_detected.value() ? 1 : 0;
    }

    // If voice_detected not available, return 0 (no info).
    return 0;
}

int apm_process_render(void* handle, const float* samples, int num_samples) {
    if (!handle || !samples || num_samples != 480) {
        return -1;
    }

    auto* h = static_cast<APMHandle*>(handle);

    // ProcessReverseStream uses the same deinterleaved API as ProcessStream.
    // We need a mutable copy since the API takes float** (even though render
    // is conceptually read-only, the API signature requires non-const).
    float render_buf[480];
    std::memcpy(render_buf, samples, 480 * sizeof(float));
    float* channel_ptr = render_buf;

    webrtc::StreamConfig stream_config(48000, 1);

    int err = h->apm->ProcessReverseStream(
        &channel_ptr,       // src: pointer to array of channel pointers
        stream_config,      // input config
        stream_config,      // output config
        &channel_ptr        // dest: same buffer
    );

    return (err == 0) ? 0 : -1;
}

void apm_destroy(void* handle) {
    if (handle) {
        auto* h = static_cast<APMHandle*>(handle);
        delete h;
    }
}

void apm_set_stream_delay(void* handle, int delay_ms) {
    if (!handle) return;
    auto* h = static_cast<APMHandle*>(handle);
    h->apm->set_stream_delay_ms(delay_ms);
}

float apm_get_speech_probability(void* handle) {
    if (!handle) return -1.0f;
    auto* h = static_cast<APMHandle*>(handle);
    auto stats = h->apm->GetStatistics();
    if (stats.voice_detected.has_value()) {
        return stats.voice_detected.value() ? 1.0f : 0.0f;
    }
    // voice_detected not populated in v2 without legacy AGC1.
    return -1.0f;
}

} // extern "C"
