package com.modemobile.ackbar_mobile

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.speech.RecognitionListener
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.util.Locale
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

class MainActivity : FlutterActivity(), TextToSpeech.OnInitListener {
    private val channelName = "dev.ackbar.app/speech"
    private var speechChannel: MethodChannel? = null
    private var tts: TextToSpeech? = null
    private var ttsReady = false
    private var speechRecognizer: SpeechRecognizer? = null
    private val mainHandler = Handler(Looper.getMainLooper())

    private val isLivePcmActive = AtomicBoolean(false)
    private var audioRecord: AudioRecord? = null
    private var audioTrack: AudioTrack? = null
    private var activeSpeakerSampleRate = 24000

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        tts = TextToSpeech(this, this)
        val channel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, channelName)
        speechChannel = channel

        channel.setMethodCallHandler { call, result ->
            when (call.method) {
                "speak" -> {
                    val text = call.argument<String>("text") ?: ""
                    speakText(text, result)
                }
                "stop" -> {
                    tts?.stop()
                    result.success(true)
                }
                "startListening" -> {
                    startDictation(result)
                }
                "stopListening" -> {
                    stopDictation()
                    result.success(true)
                }
                "startLivePcm" -> {
                    startLivePcmStream(result)
                }
                "stopLivePcm" -> {
                    stopLivePcmStream()
                    result.success(true)
                }
                "playPcm" -> {
                    val pcm = call.argument<ByteArray>("pcm")
                    val rate = call.argument<Int>("sampleRate") ?: 24000
                    if (pcm != null && pcm.isNotEmpty()) {
                        playPcmChunk(pcm, rate)
                    }
                    result.success(true)
                }
                "clearPcm" -> {
                    try {
                        audioTrack?.pause()
                        audioTrack?.flush()
                        audioTrack?.play()
                    } catch (_: Exception) {}
                    result.success(true)
                }
                else -> result.notImplemented()
            }
        }
    }

    override fun onInit(status: Int) {
        if (status == TextToSpeech.SUCCESS) {
            tts?.language = Locale.US
            tts?.setSpeechRate(1.05f)
            tts?.setOnUtteranceProgressListener(object : UtteranceProgressListener() {
                override fun onStart(utteranceId: String?) {}
                override fun onDone(utteranceId: String?) {
                    mainHandler.post {
                        speechChannel?.invokeMethod("onSpeakComplete", null)
                    }
                }
                override fun onError(utteranceId: String?) {
                    mainHandler.post {
                        speechChannel?.invokeMethod("onSpeakComplete", null)
                    }
                }
            })
            ttsReady = true
        }
    }

    private fun speakText(text: String, result: MethodChannel.Result) {
        if (text.isBlank() || !ttsReady) {
            result.success(false)
            return
        }
        val params = Bundle()
        val utteranceId = "ackbar_utt_${System.currentTimeMillis()}"
        tts?.speak(text, TextToSpeech.QUEUE_FLUSH, params, utteranceId)
        result.success(true)
    }

    private fun ensureMicPermission(): Boolean {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.RECORD_AUDIO), 4713)
            return false
        }
        return true
    }

    private fun startDictation(result: MethodChannel.Result) {
        if (!ensureMicPermission()) {
            result.success(false)
            return
        }
        stopDictation()
        if (!SpeechRecognizer.isRecognitionAvailable(this)) {
            result.success(false)
            return
        }
        val recognizer = SpeechRecognizer.createSpeechRecognizer(this)
        speechRecognizer = recognizer
        recognizer.setRecognitionListener(object : RecognitionListener {
            override fun onReadyForSpeech(params: Bundle?) {}
            override fun onBeginningOfSpeech() {}
            override fun onRmsChanged(rmsdB: Float) {}
            override fun onBufferReceived(buffer: ByteArray?) {}
            override fun onEndOfSpeech() {}
            override fun onError(error: Int) {
                mainHandler.post {
                    speechChannel?.invokeMethod("onSpeechError", mapOf("code" to error))
                }
            }
            override fun onResults(results: Bundle?) {
                val matches = results?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)
                val text = matches?.firstOrNull() ?: ""
                if (text.isNotBlank()) {
                    mainHandler.post {
                        speechChannel?.invokeMethod("onSpeechResult", mapOf("text" to text, "isFinal" to true))
                    }
                }
            }
            override fun onPartialResults(partialResults: Bundle?) {
                val matches = partialResults?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)
                val text = matches?.firstOrNull() ?: ""
                if (text.isNotBlank()) {
                    mainHandler.post {
                        speechChannel?.invokeMethod("onSpeechResult", mapOf("text" to text, "isFinal" to false))
                    }
                }
            }
            override fun onEvent(eventType: Int, params: Bundle?) {}
        })

        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            putExtra(RecognizerIntent.EXTRA_LANGUAGE, "en-US")
            putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, true)
        }
        recognizer.startListening(intent)
        result.success(true)
    }

    private fun stopDictation() {
        try {
            speechRecognizer?.stopListening()
            speechRecognizer?.destroy()
        } catch (_: Exception) {}
        speechRecognizer = null
    }

    private fun startLivePcmStream(result: MethodChannel.Result) {
        if (!ensureMicPermission()) {
            result.success(false)
            return
        }
        stopDictation()
        stopLivePcmStream()

        val micRate = 16000
        val minBuf = AudioRecord.getMinBufferSize(
            micRate,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_16BIT
        ).coerceAtLeast(2560)

        try {
            val rec = AudioRecord(
                MediaRecorder.AudioSource.VOICE_COMMUNICATION,
                micRate,
                AudioFormat.CHANNEL_IN_MONO,
                AudioFormat.ENCODING_PCM_16BIT,
                minBuf * 2
            )
            audioRecord = rec
            rec.startRecording()
            isLivePcmActive.set(true)

            ensureSpeakerTrack(activeSpeakerSampleRate)

            thread(start = true, name = "AckbarMicCapture") {
                val buf = ByteArray(1280)
                while (isLivePcmActive.get()) {
                    val n = rec.read(buf, 0, buf.size)
                    if (n > 0) {
                        val copy = buf.copyOf(n)
                        mainHandler.post {
                            if (isLivePcmActive.get()) {
                                speechChannel?.invokeMethod("onMicPcm", copy)
                            }
                        }
                    }
                }
            }
            result.success(true)
        } catch (_: Exception) {
            result.success(false)
        }
    }

    private fun ensureSpeakerTrack(sampleRate: Int) {
        if (audioTrack != null && activeSpeakerSampleRate == sampleRate) return
        try {
            audioTrack?.stop()
            audioTrack?.release()
        } catch (_: Exception) {}
        activeSpeakerSampleRate = sampleRate
        val minTrackBuf = AudioTrack.getMinBufferSize(
            sampleRate,
            AudioFormat.CHANNEL_OUT_MONO,
            AudioFormat.ENCODING_PCM_16BIT
        ).coerceAtLeast(4800)
        val track = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build()
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setSampleRate(sampleRate)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                    .build()
            )
            .setBufferSizeInBytes(minTrackBuf * 2)
            .setTransferMode(AudioTrack.MODE_STREAM)
            .build()
        track.play()
        audioTrack = track
    }

    private fun playPcmChunk(pcm: ByteArray, sampleRate: Int) {
        if (!isLivePcmActive.get()) return
        ensureSpeakerTrack(sampleRate)
        try {
            audioTrack?.write(pcm, 0, pcm.size)
        } catch (_: Exception) {}
    }

    private fun stopLivePcmStream() {
        isLivePcmActive.set(false)
        try {
            audioRecord?.stop()
            audioRecord?.release()
        } catch (_: Exception) {}
        audioRecord = null
        try {
            audioTrack?.stop()
            audioTrack?.release()
        } catch (_: Exception) {}
        audioTrack = null
    }

    override fun onDestroy() {
        stopDictation()
        stopLivePcmStream()
        tts?.stop()
        tts?.shutdown()
        super.onDestroy()
    }
}
