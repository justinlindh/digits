/*
 * rnnoise_record_play.c — Record with RNNoise, then play back through lineout
 *
 * Records N seconds from mic, runs through RNNoise in real-time,
 * stores denoised audio, then plays it back.
 *
 * Build: gcc -O2 -o rnnoise_rp rnnoise_record_play.c -L. -lrnnoise -lasound -lm
 * Usage: ./rnnoise_rp [seconds]           # default 5s
 *        ./rnnoise_rp [seconds] --raw     # also save raw (no denoise) for comparison
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
    int duration = 5;
    int save_raw = 0;

    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--raw") == 0) save_raw = 1;
        else duration = atoi(argv[i]);
    }
    if (duration < 1) duration = 5;
    if (duration > 30) duration = 30;

    signal(SIGINT, sighandler);

    int total_frames = (SAMPLE_RATE * duration) / FRAME_SIZE;
    int total_samples = total_frames * FRAME_SIZE;

    int16_t *denoised = calloc(total_samples, sizeof(int16_t));
    int16_t *raw_audio = save_raw ? calloc(total_samples, sizeof(int16_t)) : NULL;
    if (!denoised) { fprintf(stderr, "OOM\n"); return 1; }

    /* Open capture (stereo, hw:Zero) */
    snd_pcm_t *cap;
    snd_pcm_hw_params_t *params;
    unsigned int rate = SAMPLE_RATE;

    if (snd_pcm_open(&cap, "hw:Zero", SND_PCM_STREAM_CAPTURE, 0) < 0) {
        fprintf(stderr, "Cannot open capture\n"); return 1;
    }
    snd_pcm_hw_params_alloca(&params);
    snd_pcm_hw_params_any(cap, params);
    snd_pcm_hw_params_set_access(cap, params, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(cap, params, SND_PCM_FORMAT_S16_LE);
    snd_pcm_hw_params_set_channels(cap, params, 2);
    snd_pcm_hw_params_set_rate_near(cap, params, &rate, 0);
    snd_pcm_hw_params(cap, params);

    /* Create RNNoise */
    DenoiseState *st = rnnoise_create(NULL);

    int16_t stereo_buf[FRAME_SIZE * 2];
    float rnn_buf[FRAME_SIZE];

    fprintf(stderr, "🎙️  Recording %d seconds... speak into handset\n", duration);

    for (int f = 0; f < total_frames && running; f++) {
        int n = snd_pcm_readi(cap, stereo_buf, FRAME_SIZE);
        if (n == -EPIPE) { snd_pcm_prepare(cap); f--; continue; }
        if (n < 0) { fprintf(stderr, "Read error: %s\n", snd_strerror(n)); break; }
        if (n != FRAME_SIZE) { f--; continue; }

        /* Extract right channel (mic is on Mixin Right / channel 1) */
        for (int i = 0; i < FRAME_SIZE; i++) {
            int16_t sample = stereo_buf[i * 2 + 1];
            rnn_buf[i] = (float)sample;
            if (raw_audio) raw_audio[f * FRAME_SIZE + i] = sample;
        }

        /* Denoise */
        rnnoise_process_frame(st, rnn_buf, rnn_buf);

        /* Store denoised */
        for (int i = 0; i < FRAME_SIZE; i++) {
            float s = rnn_buf[i];
            if (s > 32767.f) s = 32767.f;
            if (s < -32768.f) s = -32768.f;
            denoised[f * FRAME_SIZE + i] = (int16_t)s;
        }

        if ((f + 1) % 100 == 0)
            fprintf(stderr, "\r  %d/%d frames", f + 1, total_frames);
    }

    snd_pcm_close(cap);
    fprintf(stderr, "\n✅ Recording complete\n");

    /* Save raw file if requested */
    if (raw_audio) {
        FILE *fp = fopen("/tmp/raw_audio.raw", "wb");
        if (fp) { fwrite(raw_audio, sizeof(int16_t), total_samples, fp); fclose(fp); }
        fprintf(stderr, "📁 Raw saved: /tmp/raw_audio.raw\n");
        free(raw_audio);
    }

    /* Save denoised */
    {
        FILE *fp = fopen("/tmp/denoised_audio.raw", "wb");
        if (fp) { fwrite(denoised, sizeof(int16_t), total_samples, fp); fclose(fp); }
        fprintf(stderr, "📁 Denoised saved: /tmp/denoised_audio.raw\n");
    }

    /* Playback through lineout (mono) */
    fprintf(stderr, "\n🔊 Playing back DENOISED audio through lineout...\n");

    snd_pcm_t *play;
    if (snd_pcm_open(&play, "hw:Zero", SND_PCM_STREAM_PLAYBACK, 0) < 0) {
        fprintf(stderr, "Cannot open playback, trying default\n");
        if (snd_pcm_open(&play, "default", SND_PCM_STREAM_PLAYBACK, 0) < 0) {
            fprintf(stderr, "Playback failed\n");
            goto done;
        }
    }

    snd_pcm_hw_params_any(play, params);
    snd_pcm_hw_params_set_access(play, params, SND_PCM_ACCESS_RW_INTERLEAVED);
    snd_pcm_hw_params_set_format(play, params, SND_PCM_FORMAT_S16_LE);
    /* Try mono first, fall back to stereo */
    unsigned int play_channels = 1;
    if (snd_pcm_hw_params_set_channels(play, params, 1) < 0) {
        play_channels = 2;
        snd_pcm_hw_params_set_channels(play, params, 2);
    }
    rate = SAMPLE_RATE;
    snd_pcm_hw_params_set_rate_near(play, params, &rate, 0);
    if (snd_pcm_hw_params(play, params) < 0) {
        fprintf(stderr, "Playback hw_params failed\n");
        snd_pcm_close(play);
        goto done;
    }
    fprintf(stderr, "Playback: %u channels at %u Hz\n", play_channels, rate);

    int16_t play_buf[FRAME_SIZE * 2]; /* stereo if needed */
    for (int f = 0; f < total_frames && running; f++) {
        if (play_channels == 2) {
            for (int i = 0; i < FRAME_SIZE; i++) {
                play_buf[i * 2] = denoised[f * FRAME_SIZE + i];
                play_buf[i * 2 + 1] = denoised[f * FRAME_SIZE + i];
            }
            snd_pcm_writei(play, play_buf, FRAME_SIZE);
        } else {
            snd_pcm_writei(play, &denoised[f * FRAME_SIZE], FRAME_SIZE);
        }
    }
    snd_pcm_drain(play);
    snd_pcm_close(play);
    fprintf(stderr, "✅ Playback complete\n");

    if (save_raw) {
        fprintf(stderr, "\n🔊 Playing back RAW audio for comparison...\n");
        raw_audio = calloc(total_samples, sizeof(int16_t));
        FILE *fp = fopen("/tmp/raw_audio.raw", "rb");
        if (fp && raw_audio) {
            fread(raw_audio, sizeof(int16_t), total_samples, fp);
            fclose(fp);

            if (snd_pcm_open(&play, "hw:Zero", SND_PCM_STREAM_PLAYBACK, 0) == 0) {
                snd_pcm_hw_params_any(play, params);
                snd_pcm_hw_params_set_access(play, params, SND_PCM_ACCESS_RW_INTERLEAVED);
                snd_pcm_hw_params_set_format(play, params, SND_PCM_FORMAT_S16_LE);
                snd_pcm_hw_params_set_channels(play, params, play_channels);
                rate = SAMPLE_RATE;
                snd_pcm_hw_params_set_rate_near(play, params, &rate, 0);
                snd_pcm_hw_params(play, params);

                for (int f = 0; f < total_frames && running; f++) {
                    if (play_channels == 2) {
                        for (int i = 0; i < FRAME_SIZE; i++) {
                            play_buf[i * 2] = raw_audio[f * FRAME_SIZE + i];
                            play_buf[i * 2 + 1] = raw_audio[f * FRAME_SIZE + i];
                        }
                        snd_pcm_writei(play, play_buf, FRAME_SIZE);
                    } else {
                        snd_pcm_writei(play, &raw_audio[f * FRAME_SIZE], FRAME_SIZE);
                    }
                }
                snd_pcm_drain(play);
                snd_pcm_close(play);
                fprintf(stderr, "✅ Raw playback complete\n");
            }
            free(raw_audio);
        }
    }

done:
    rnnoise_destroy(st);
    free(denoised);
    return 0;
}
