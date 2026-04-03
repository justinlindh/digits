CC      = gcc
CFLAGS  = -O2 -Wall
ARCH    = $(shell uname -m)
ifeq ($(ARCH),x86_64)
  LIBDIR = lib/x86_64
else
  LIBDIR = lib/aarch64
endif
LDFLAGS = -L$(LIBDIR) -lrnnoise -lasound -lm

BINS = rnnoise_bench rnnoise_rp rnnoise_loop

all: $(BINS)

rnnoise_bench: rnnoise_bench.c $(LIBDIR)/librnnoise.a rnnoise.h
	$(CC) $(CFLAGS) -o $@ $< $(LDFLAGS)

rnnoise_rp: rnnoise_record_play.c $(LIBDIR)/librnnoise.a rnnoise.h
	$(CC) $(CFLAGS) -o $@ $< $(LDFLAGS)

rnnoise_loop: rnnoise_loopback.c $(LIBDIR)/librnnoise.a rnnoise.h
	$(CC) $(CFLAGS) -o $@ $< $(LDFLAGS)

clean:
	rm -f $(BINS)

.PHONY: all clean
