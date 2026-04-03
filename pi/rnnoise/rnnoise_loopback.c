/*
 * rnnoise_loopback.c — Real-time mic → RNNoise → earpiece loopback
 *
 * Captures from Codec Zero mic, denoises, plays back immediately.
 * Ctrl+C to stop.
 *
 * Build: gcc -O2 -o rnnoise_loop rnnoise_loopback.c -L. -lrnnoise -lasound -lm
 * Usage: ./rnnoise_loop           # denoised loopback
 *        ./rnnoise_loop --raw     # raw loopback (no denoise, for comparison)
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <signal.h>
#include <alsa/asoundlib.h>
#include "rnnoise.h"

#define FRAME_SIZE 480
#define SAMPLE_RATE 48000

static volatile int running = 1;
static void sighandler(int sig) { (void)sig; running = 0; }

int main(int argc, char **argv) {
    int raw_mode = 0;
    for (int i = 1; i < argc; i++)
        if (strcmp(argv[i], "--raw") == 0) raw_mode = 1;

    signal(SIGINT, sighandler);
    signal(SIGTERM, sighandler);

    /* Open capture (stereo, hw:Zero) */
    snd_pcm_t *cap;
    snd_pcm_hw_params_t *cap_params;
    unsigned int rate = SAMPLE_RATE;

    if (snd_pcm_open(&cap, "hw:Zero", SND_PCM_STREAM_CAPTURE, 0) < 0) {
        fprintf(stderr, "Cannot open capture\n"); return 1;
    }
    snd_pcm_hw_params_alloca(&cap_params);
    snd_pcm_hw_params_any(cap, cap_params);
    snd_pcm_hw_params_set_access(cap, cap_params, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(cap, cap_params, SND_PCM_FORMAT_S16_LE);
    snd_pcm_hw_params_set_channels(cap, cap_params, 2);
    snd_pcm_hw_params_set_rate_near(cap, cap_params, &rate, 0);
    /* Tight buffer for low latency */
    snd_pcm_uframes_t cap_period = FRAME_SIZE;
    snd_pcm_uframes_t cap_buffer = FRAME_SIZE * 3;
    snd_pcm_hw_params_set_period_size_near(cap, cap_params, &cap_period, 0);
    snd_pcm_hw_params_set_buffer_size_near(cap, cap_params, &cap_buffer);
    if (snd_pcm_hw_params(cap, cap_params) < 0) {
        fprintf(stderr, "Capture hw_params failed\n"); return 1;
    }

    /* Open playback (hw:Zero) */
    snd_pcm_t *play;
    snd_pcm_hw_params_t *play_params;

    if (snd_pcm_open(&play, "hw:Zero", SND_PCM_STREAM_PLAYBACK, 0) < 0) {
        fprintf(stderr, "Cannot open playback\n"); return 1;
    }
    snd_pcm_hw_params_alloca(&play_params);
    snd_pcm_hw_params_any(play, play_params);
    snd_pcm_hw_params_set_access(play, play_params, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(play, play_params, SND_PCM_FORMAT_S16_LE);
    /* Playback may need stereo too */
    unsigned int play_channels = 2;
    if (snd_pcm_hw_params_set_channels(play, play_params, 2) < 0) {
        play_channels = 1;
        snd_pcm_hw_params_set_channels(play, play_params, 1);
    }
    rate = SAMPLE_RATE;
    snd_pcm_hw_params_set_rate_near(play, play_params, &rate, 0);
    snd_pcm_uframes_t play_period = FRAME_SIZE;
    snd_pcm_uframes_t play_buffer = FRAME_SIZE * 3;
    snd_pcm_hw_params_set_period_size_near(play, play_params, &play_period, 0);
    snd_pcm_hw_params_set_buffer_size_near(play, play_params, &play_buffer);
    if (snd_pcm_hw_params(play, play_params) < 0) {
        fprintf(stderr, "Playback hw_params failed\n"); return 1;
    }

    fprintf(stderr, "Playback: %u channels\n", play_channels);

    /* Create RNNoise */
    DenoiseState *st = raw_mode ? NULL : rnnoise_create(NULL);

    int16_t stereo_in[FRAME_SIZE * 2];
    float rnn_buf[FRAME_SIZE];
    int16_t play_buf[FRAME_SIZE * 2];

    int frames = 0;
    int overruns = 0, underruns = 0;

    fprintf(stderr, "\n🎧 %s loopback running — Ctrl+C to stop\n",
            raw_mode ? "RAW" : "DENOISED");
    fprintf(stderr, "Latency: ~%.0fms (capture + process + playback)\n\n",
            (double)(cap_period + play_period) / SAMPLE_RATE * 1000.0 + (raw_mode ? 0 : 4));

    while (running) {
        /* Capture one frame */
        int n = snd_pcm_readi(cap, stereo_in, FRAME_SIZE);
        if (n == -EPIPE) { snd_pcm_prepare(cap); overruns++; continue; }
        if (n < 0) break;
        if (n != FRAME_SIZE) continue;

        /* Extract left channel */
        for (int i = 0; i < FRAME_SIZE; i++)
            rnn_buf[i] = (float)stereo_in[i * 2];

        /* Denoise (unless raw mode) */
        if (st)
            rnnoise_process_frame(st, rnn_buf, rnn_buf);

        /* Prepare playback buffer */
        for (int i = 0; i < FRAME_SIZE; i++) {
            float s = rnn_buf[i];
            if (s > 32767.f) s = 32767.f;
            if (s < -32768.f) s = -32768.f;
            int16_t sample = (int16_t)s;
            if (play_channels == 2) {
                play_buf[i * 2] = sample;
                play_buf[i * 2 + 1] = sample;
            } else {
                play_buf[i] = sample;
            }
        }

        /* Write to playback */
        int w = snd_pcm_writei(play, play_buf, FRAME_SIZE);
        if (w == -EPIPE) { snd_pcm_prepare(play); underruns++; }

        frames++;
        if (frames % 500 == 0)
            fprintf(stderr, "\r  %d frames | overruns=%d underruns=%d  ",
                    frames, overruns, underruns);
    }

    fprintf(stderr, "\n\nTotal: %d frames (%.1fs) | overruns=%d underruns=%d\n",
            frames, (double)frames * FRAME_SIZE / SAMPLE_RATE, overruns, underruns);

    if (st) rnnoise_destroy(st);
    snd_pcm_close(cap);
    snd_pcm_close(play);
    return 0;
}
