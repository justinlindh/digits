package audio

// RNNoiseFrameSize is the number of samples per RNNoise frame (10ms at 48kHz).
const RNNoiseFrameSize = 480

// int16ToFloat32 converts int16 PCM samples to float32.
// RNNoise expects raw int16 values cast to float32 (NOT normalized to [-1,1]).
func int16ToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, s := range in {
		out[i] = float32(s)
	}
	return out
}

// float32ToInt16 converts float32 samples back to int16, clamping to [-32768, 32767].
func float32ToInt16(in []float32) []int16 {
	out := make([]int16, len(in))
	for i, s := range in {
		if s > 32767 {
			out[i] = 32767
		} else if s < -32768 {
			out[i] = -32768
		} else {
			out[i] = int16(s)
		}
	}
	return out
}
