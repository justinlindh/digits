/*
 * rnnoise_bench.c — Real-time RNNoise benchmark for Pi Zero 2 W
 *
 * Captures from ALSA mic, processes through RNNoise, measures timing.
 * Optionally plays back denoised audio.
 *
 * Build on Pi:
 *   gcc -O2 -o rnnoise_bench rnnoise_bench.c -L. -lrnnoise -lasound -lm
 *
 * Usage:
 *   ./rnnoise_bench              # benchmark only (10 seconds)
 *   ./rnnoise_bench --playback   # benchmark + play denoised audio
 *   ./rnnoise_bench --record out.raw  # benchmark + save denoised to file
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <math.h>
#include <signal.h>
#include <alsa/asoundlib.h>
#include "rnnoise.h"

/* RNNoise frame size is fixed at 480 samples (10ms at 48kHz) */
#define FRAME_SIZE 480
#define SAMPLE_RATE 48000
#define TEST_DURATION_SEC 10

static volatile int running = 1;

static void sighandler(int sig) {
    (void)sig;
    running = 0;
}

static snd_pcm_t *open_alsa(const char *device, snd_pcm_stream_t stream) {
    snd_pcm_t *pcm = NULL;
    snd_pcm_hw_params_t *params;
    int err;
    unsigned int rate = SAMPLE_RATE;

    if ((err = snd_pcm_open(&pcm, device, stream, 0)) < 0) {
        fprintf(stderr, "ALSA open %s failed: %s\n", device, snd_strerror(err));
        return NULL;
    }

    snd_pcm_hw_params_alloca(&params);
    snd_pcm_hw_params_any(pcm, params);
    snd_pcm_hw_params_set_access(pcm, params, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(pcm, params, SND_PCM_FORMAT_S16_LE);
    unsigned int channels = (stream == SND_PCM_STREAM_CAPTURE) ? 2 : 1;
    snd_pcm_hw_params_set_channels(pcm, params, channels);
    snd_pcm_hw_params_set_rate_near(pcm, params, &rate, 0);

    /* Use small buffer for low latency */
    snd_pcm_uframes_t buffer_size = FRAME_SIZE * 4;
    snd_pcm_uframes_t period_size = FRAME_SIZE;
    snd_pcm_hw_params_set_buffer_size_near(pcm, params, &buffer_size);
    snd_pcm_hw_params_set_period_size_near(pcm, params, &period_size, 0);

    if ((err = snd_pcm_hw_params(pcm, params)) < 0) {
        fprintf(stderr, "ALSA hw_params failed: %s\n", snd_strerror(err));
        snd_pcm_close(pcm);
        return NULL;
    }

    /* Report actual settings */
    snd_pcm_hw_params_get_rate(params, &rate, 0);
    snd_pcm_hw_params_get_buffer_size(params, &buffer_size);
    snd_pcm_hw_params_get_period_size(params, &period_size, 0);
    const char *dir = (stream == SND_PCM_STREAM_CAPTURE) ? "capture" : "playback";
    fprintf(stderr, "ALSA %s: rate=%u buffer=%lu period=%lu\n",
            dir, rate, buffer_size, period_size);

    return pcm;
}

static double timespec_diff_ms(struct timespec *start, struct timespec *end) {
    return (end->tv_sec - start->tv_sec) * 1000.0 +
           (end->tv_nsec - start->tv_nsec) / 1e6;
}

int main(int argc, char **argv) {
    int do_playback = 0;
    const char *record_path = NULL;
    FILE *record_fp = NULL;

    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--playback") == 0) do_playback = 1;
        else if (strcmp(argv[i], "--record") == 0 && i + 1 < argc)
            record_path = argv[++i];
    }

    signal(SIGINT, sighandler);
    signal(SIGTERM, sighandler);

    /* Open capture */
    snd_pcm_t *cap = open_alsa("hw:Zero", SND_PCM_STREAM_CAPTURE);
    if (!cap) {
        fprintf(stderr, "Trying 'default' device...\n");
        cap = open_alsa("default", SND_PCM_STREAM_CAPTURE);
    }
    if (!cap) return 1;

    /* Open playback if requested */
    snd_pcm_t *play = NULL;
    if (do_playback) {
        play = open_alsa("default", SND_PCM_STREAM_PLAYBACK);
        if (!play) fprintf(stderr, "Warning: playback open failed, continuing without\n");
    }

    if (record_path) {
        record_fp = fopen(record_path, "wb");
        if (!record_fp) {
            fprintf(stderr, "Cannot open %s for writing\n", record_path);
            return 1;
        }
    }

    /* Create RNNoise state */
    DenoiseState *st = rnnoise_create(NULL);
    if (!st) {
        fprintf(stderr, "rnnoise_create failed\n");
        return 1;
    }

    int16_t pcm_buf[FRAME_SIZE * 2]; /* stereo capture */
    float rnn_in[FRAME_SIZE];
    int16_t pcm_out[FRAME_SIZE];

    int frames_processed = 0;
    double total_process_ms = 0.0;
    double max_process_ms = 0.0;
    double min_process_ms = 1e9;
    double frame_budget_ms = (double)FRAME_SIZE / SAMPLE_RATE * 1000.0; /* 10ms */

    int overruns = 0;
    struct timespec t_start, t_end, t_begin;
    clock_gettime(CLOCK_MONOTONIC, &t_begin);

    fprintf(stderr, "\n=== RNNoise Real-Time Benchmark ===\n");
    fprintf(stderr, "Frame: %d samples (%.1fms at %dHz)\n",
            FRAME_SIZE, frame_budget_ms, SAMPLE_RATE);
    fprintf(stderr, "Running for %d seconds...\n\n", TEST_DURATION_SEC);

    while (running) {
        /* Check duration */
        struct timespec t_now;
        clock_gettime(CLOCK_MONOTONIC, &t_now);
        if (timespec_diff_ms(&t_begin, &t_now) > TEST_DURATION_SEC * 1000.0)
            break;

        /* Read one frame from mic */
        int n = snd_pcm_readi(cap, pcm_buf, FRAME_SIZE);
        if (n == -EPIPE) {
            fprintf(stderr, "ALSA overrun!\n");
            snd_pcm_prepare(cap);
            overruns++;
            continue;
        }
        if (n < 0) {
            fprintf(stderr, "ALSA read error: %s\n", snd_strerror(n));
            break;
        }
        if (n != FRAME_SIZE) continue;

        /* Convert S16 stereo → float mono (left channel only) */
        for (int i = 0; i < FRAME_SIZE; i++)
            rnn_in[i] = (float)pcm_buf[i * 2];

        /* Time the RNNoise processing */
        clock_gettime(CLOCK_MONOTONIC, &t_start);
        float vad = rnnoise_process_frame(st, rnn_in, rnn_in);
        clock_gettime(CLOCK_MONOTONIC, &t_end);

        double elapsed = timespec_diff_ms(&t_start, &t_end);
        total_process_ms += elapsed;
        if (elapsed > max_process_ms) max_process_ms = elapsed;
        if (elapsed < min_process_ms) min_process_ms = elapsed;
        frames_processed++;

        /* Convert float → S16 */
        for (int i = 0; i < FRAME_SIZE; i++) {
            float s = rnn_in[i];
            if (s > 32767.f) s = 32767.f;
            if (s < -32768.f) s = -32768.f;
            pcm_out[i] = (int16_t)s;
        }

        /* Playback */
        if (play) {
            int w = snd_pcm_writei(play, pcm_out, FRAME_SIZE);
            if (w == -EPIPE) snd_pcm_prepare(play);
        }

        /* Record */
        if (record_fp) {
            fwrite(pcm_out, sizeof(int16_t), FRAME_SIZE, record_fp);
        }

        /* Progress every second */
        if (frames_processed % 100 == 0) {
            double avg = total_process_ms / frames_processed;
            fprintf(stderr, "\r  %d frames | avg=%.2fms | max=%.2fms | budget=%.1fms | CPU=%.1f%% | VAD=%.2f  ",
                    frames_processed, avg, max_process_ms, frame_budget_ms,
                    (avg / frame_budget_ms) * 100.0, vad);
        }
    }

    fprintf(stderr, "\n\n=== Results ===\n");
    if (frames_processed > 0) {
        double avg = total_process_ms / frames_processed;
        fprintf(stderr, "Frames processed:  %d (%.1f seconds)\n",
                frames_processed, (double)frames_processed * FRAME_SIZE / SAMPLE_RATE);
        fprintf(stderr, "Frame budget:      %.2f ms\n", frame_budget_ms);
        fprintf(stderr, "Avg process time:  %.2f ms (%.1f%% of budget)\n",
                avg, (avg / frame_budget_ms) * 100.0);
        fprintf(stderr, "Min process time:  %.2f ms\n", min_process_ms);
        fprintf(stderr, "Max process time:  %.2f ms\n", max_process_ms);
        fprintf(stderr, "ALSA overruns:     %d\n", overruns);
        fprintf(stderr, "Verdict:           %s\n",
                max_process_ms < frame_budget_ms ? "✅ REAL-TIME CAPABLE" :
                avg < frame_budget_ms ? "⚠️  MARGINAL (avg OK, spikes exceed budget)" :
                "❌ TOO SLOW FOR REAL-TIME");
    }

    rnnoise_destroy(st);
    snd_pcm_close(cap);
    if (play) snd_pcm_close(play);
    if (record_fp) fclose(record_fp);

    return 0;
}
