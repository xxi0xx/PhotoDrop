# Video fixtures

These original fixtures contain one black 16×16 H.264 frame, no audio, and no
personal metadata. MP4 is 1509 bytes; MOV is 1460 bytes. Both are committed so
tests need no encoder, downloads, or third-party footage. They are licensed with
the repository. Neither fixture nor an encoder is included in the runtime image.

They were generated once using the encoder already inside the pinned real-Immich
test image (`ghcr.io/immich-app/immich-server:v3.2.1`):

```sh
ffmpeg -f lavfi -i color=c=black:s=16x16:r=1 -frames:v 1 -c:v libx264 \
  -pix_fmt yuv420p -flags +bitexact -fflags +bitexact -map_metadata -1 \
  -movflags +faststart tiny.mp4
ffmpeg -i tiny.mp4 -c copy -map_metadata -1 -fflags +bitexact tiny.mov
```

PhotoDrop does not invoke this tool, transcode, or decode video. The real Immich
suite imports both containers and verifies original-byte hashes on readback.
