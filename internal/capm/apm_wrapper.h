#ifndef APM_WRAPPER_H
#define APM_WRAPPER_H

#ifdef __cplusplus
extern "C" {
#endif

/* Creates an APM instance configured for capture processing.
 * NS: high, AGC2 adaptive digital: on, high-pass: on, AEC: off.
 * Returns opaque handle or NULL on failure. */
void* apm_create(void);

/* Creates an APM instance with echo cancellation enabled (AEC3).
 * NS: high, AGC2 adaptive digital: on, high-pass: on, AEC3: on.
 * Use with apm_process_render() to feed speaker reference frames.
 * Returns opaque handle or NULL on failure. */
void* apm_create_with_aec(void);

/* Processes a 10ms capture frame (480 float32 samples at 48kHz mono).
 * Samples are modified in-place (denoised, gained, echo-cancelled if AEC enabled).
 * Returns: 1 if voice detected, 0 if not, -1 on error. */
int apm_process_capture(void* handle, float* samples, int num_samples);

/* Processes a 10ms render (speaker) frame as AEC reference signal.
 * Must be called BEFORE the corresponding ProcessCapture for proper echo cancellation.
 * samples: 480 float32 at 48kHz mono (not modified in-place).
 * Returns: 0 on success, -1 on error. */
int apm_process_render(void* handle, const float* samples, int num_samples);

/* Destroys the APM instance. Safe to call with NULL. */
void apm_destroy(void* handle);

/* Sets the stream delay in milliseconds (for AEC alignment). */
void apm_set_stream_delay(void* handle, int delay_ms);

/* Returns speech probability: 1.0 if voice detected, 0.0 if not, -1.0 if unavailable.
 * Note: voice_detected is deprecated in v2 and may not be populated. */
float apm_get_speech_probability(void* handle);

#ifdef __cplusplus
}
#endif

#endif /* APM_WRAPPER_H */
