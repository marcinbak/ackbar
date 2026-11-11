import AVFoundation
import Flutter
import Speech
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate, AVSpeechSynthesizerDelegate {
  private var speechChannel: FlutterMethodChannel?
  private var launcherChannel: FlutterMethodChannel?
  private let synthesizer = AVSpeechSynthesizer()
  private let speechRecognizer = SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
  private var recognitionRequest: SFSpeechAudioBufferRecognitionRequest?
  private var recognitionTask: SFSpeechRecognitionTask?
  private let audioEngine = AVAudioEngine()
  private var livePlayerNode: AVAudioPlayerNode?
  private var livePlayerFormat: AVAudioFormat?
  private var isLivePcmActive = false

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    synthesizer.delegate = self
    if let controller = window?.rootViewController as? FlutterViewController {
      setupSpeechChannel(messenger: controller.binaryMessenger)
      setupLauncherChannel(messenger: controller.binaryMessenger)
    }
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    setupSpeechChannel(messenger: engineBridge.applicationRegistrar.messenger())
    setupLauncherChannel(messenger: engineBridge.applicationRegistrar.messenger())
  }

  private func setupLauncherChannel(messenger: FlutterBinaryMessenger) {
    guard launcherChannel == nil else { return }
    let channel = FlutterMethodChannel(name: "dev.ackbar.app/launcher", binaryMessenger: messenger)
    launcherChannel = channel
    channel.setMethodCallHandler { call, result in
      switch call.method {
      case "openExternalUrl":
        let args = call.arguments as? [String: Any]
        guard let raw = args?["url"] as? String else {
          result(false)
          return
        }
        let urlStr = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if urlStr.isEmpty || urlStr.count > 4096 || urlStr.unicodeScalars.contains(where: {
          CharacterSet.whitespacesAndNewlines.contains($0) || CharacterSet.controlCharacters.contains($0)
        }) {
          result(false)
          return
        }
        guard let url = URL(string: urlStr),
              url.scheme?.lowercased() == "https",
              let host = url.host,
              !host.isEmpty,
              url.user == nil,
              url.password == nil else {
          result(false)
          return
        }
        UIApplication.shared.open(url, options: [:]) { ok in
          result(ok)
        }
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  private func setupSpeechChannel(messenger: FlutterBinaryMessenger) {
    guard speechChannel == nil else { return }
    let channel = FlutterMethodChannel(name: "dev.ackbar.app/speech", binaryMessenger: messenger)
    speechChannel = channel
    channel.setMethodCallHandler { [weak self] call, result in
      guard let self = self else {
        result(nil)
        return
      }
      switch call.method {
      case "speak":
        let args = call.arguments as? [String: Any]
        let text = args?["text"] as? String ?? ""
        self.speakText(text, result: result)
      case "stop":
        if self.synthesizer.isSpeaking {
          self.synthesizer.stopSpeaking(at: .immediate)
        }
        result(true)
      case "startListening":
        self.startSpeechDictation(result: result)
      case "stopListening":
        self.stopSpeechDictation()
        result(true)
      case "startLivePcm":
        self.startLivePcmStream(result: result)
      case "stopLivePcm":
        self.stopLivePcmStream()
        result(true)
      case "playPcm":
        let args = call.arguments as? [String: Any]
        let pcmData = (args?["pcm"] as? FlutterStandardTypedData)?.data ?? Data()
        let sampleRate = args?["sampleRate"] as? Int ?? 24000
        self.playPcmChunk(pcmData, sampleRate: Double(sampleRate))
        result(true)
      case "clearPcm":
        self.livePlayerNode?.stop()
        if self.isLivePcmActive {
          self.livePlayerNode?.play()
        }
        result(true)
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  private func speakText(_ text: String, result: @escaping FlutterResult) {
    guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      result(false)
      return
    }
    if synthesizer.isSpeaking {
      synthesizer.stopSpeaking(at: .immediate)
    }
    try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio, options: [.duckOthers])
    try? AVAudioSession.sharedInstance().setActive(true)
    let utterance = AVSpeechUtterance(string: text)
    utterance.rate = 0.52
    utterance.voice = AVSpeechSynthesisVoice(language: "en-US")
    synthesizer.speak(utterance)
    result(true)
  }

  func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didFinish utterance: AVSpeechUtterance) {
    DispatchQueue.main.async { [weak self] in
      self?.speechChannel?.invokeMethod("onSpeakComplete", arguments: nil)
    }
  }

  private func startSpeechDictation(result: @escaping FlutterResult) {
    SFSpeechRecognizer.requestAuthorization { [weak self] authStatus in
      DispatchQueue.main.async {
        guard let self = self, authStatus == .authorized else {
          result(false)
          return
        }
        self.stopSpeechDictation()
        let audioSession = AVAudioSession.sharedInstance()
        try? audioSession.setCategory(.playAndRecord, mode: .measurement, options: [.defaultToSpeaker, .allowBluetooth])
        try? audioSession.setActive(true, options: .notifyOthersOnDeactivation)

        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        self.recognitionRequest = request

        let inputNode = self.audioEngine.inputNode
        let recordingFormat = inputNode.outputFormat(forBus: 0)
        inputNode.removeTap(onBus: 0)
        inputNode.installTap(onBus: 0, bufferSize: 1024, format: recordingFormat) { buffer, _ in
          request.append(buffer)
        }

        self.audioEngine.prepare()
        try? self.audioEngine.start()

        self.recognitionTask = self.speechRecognizer?.recognitionTask(with: request) { [weak self] res, error in
          guard let self = self else { return }
          if let res = res {
            let text = res.bestTranscription.formattedString
            let isFinal = res.isFinal
            DispatchQueue.main.async {
              self.speechChannel?.invokeMethod("onSpeechResult", arguments: [
                "text": text,
                "isFinal": isFinal
              ])
            }
            if isFinal {
              self.stopSpeechDictation()
            }
          }
          if error != nil {
            self.stopSpeechDictation()
          }
        }
        result(true)
      }
    }
  }

  private func stopSpeechDictation() {
    if audioEngine.isRunning && !isLivePcmActive {
      audioEngine.stop()
      audioEngine.inputNode.removeTap(onBus: 0)
    }
    recognitionRequest?.endAudio()
    recognitionRequest = nil
    recognitionTask?.cancel()
    recognitionTask = nil
  }

  private func startLivePcmStream(result: @escaping FlutterResult) {
    stopSpeechDictation()
    stopLivePcmStream()

    let session = AVAudioSession.sharedInstance()
    try? session.setCategory(.playAndRecord, mode: .voiceChat, options: [.defaultToSpeaker, .allowBluetooth])
    try? session.setActive(true)

    let inputNode = audioEngine.inputNode
    let hwFormat = inputNode.inputFormat(forBus: 0)
    guard let targetFormat = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: true),
          let converter = AVAudioConverter(from: hwFormat, to: targetFormat) else {
      result(false)
      return
    }

    let player = AVAudioPlayerNode()
    audioEngine.attach(player)
    let speakerFormat = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 24000, channels: 1, interleaved: false)!
    audioEngine.connect(player, to: audioEngine.mainMixerNode, format: speakerFormat)
    livePlayerNode = player
    livePlayerFormat = speakerFormat

    inputNode.removeTap(onBus: 0)
    inputNode.installTap(onBus: 0, bufferSize: 2048, format: hwFormat) { [weak self] buffer, _ in
      guard let self = self, self.isLivePcmActive else { return }
      let ratio = 16000.0 / hwFormat.sampleRate
      let outFrames = AVAudioFrameCount(Double(buffer.frameLength) * ratio) + 16
      guard let outBuffer = AVAudioPCMBuffer(pcmFormat: targetFormat, frameCapacity: outFrames) else { return }
      var error: NSError?
      var consumed = false
      converter.convert(to: outBuffer, error: &error) { _, outStatus in
        if consumed {
          outStatus.pointee = .noDataNow
          return nil
        }
        consumed = true
        outStatus.pointee = .haveData
        return buffer
      }
      if error == nil, let int16Ptr = outBuffer.int16ChannelData?[0] {
        let byteCount = Int(outBuffer.frameLength) * 2
        let data = Data(bytes: int16Ptr, count: byteCount)
        DispatchQueue.main.async {
          self.speechChannel?.invokeMethod("onMicPcm", arguments: FlutterStandardTypedData(bytes: data))
        }
      }
    }

    audioEngine.prepare()
    try? audioEngine.start()
    player.play()
    isLivePcmActive = true
    result(true)
  }

  private func stopLivePcmStream() {
    isLivePcmActive = false
    livePlayerNode?.stop()
    if audioEngine.isRunning {
      audioEngine.stop()
      audioEngine.inputNode.removeTap(onBus: 0)
    }
    if let player = livePlayerNode {
      audioEngine.detach(player)
    }
    livePlayerNode = nil
  }

  private func playPcmChunk(_ data: Data, sampleRate: Double) {
    guard isLivePcmActive, let player = livePlayerNode, data.count >= 2 else { return }
    let frameCount = AVAudioFrameCount(data.count / 2)
    guard let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: sampleRate, channels: 1, interleaved: false),
          let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frameCount),
          let channelData = buffer.floatChannelData?[0] else { return }
    buffer.frameLength = frameCount
    data.withUnsafeBytes { rawBuf in
      guard let int16Ptr = rawBuf.bindMemory(to: Int16.self).baseAddress else { return }
      for i in 0..<Int(frameCount) {
        channelData[i] = Float(int16Ptr[i]) / 32768.0
      }
    }
    player.scheduleBuffer(buffer, completionHandler: nil)
  }
}
