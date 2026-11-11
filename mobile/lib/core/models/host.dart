import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../widgets/ackbar_app_bar.dart';
import '../widgets/status_badge.dart';

/// Representation of a supervised host machine running ackbard
class HostRecord {
  final String name;
  final String url;
  final String sshTarget;
  final String remoteCwd;
  final bool online;
  final int latencyMs;
  final String version;
  final String uptime;
  final int sessionsCount;
  final String tailscaleIp;
  final String authToken;
  final DateTime createdAt;

  const HostRecord({
    required this.name,
    required this.url,
    this.sshTarget = '',
    this.remoteCwd = '~/Work',
    this.online = true,
    this.latencyMs = 1,
    this.version = '',
    this.uptime = 'Active',
    this.sessionsCount = 0,
    this.tailscaleIp = '',
    this.authToken = '',
    required this.createdAt,
  });

  bool get hasAuth => authToken.isNotEmpty;

  bool get isLocal => name.toLowerCase() == 'local' || url.contains('127.0.0.1:7777');

  String get displayName => name;

  String get endpointDisplay {
    if (tailscaleIp.isNotEmpty) return tailscaleIp;
    if (sshTarget.isNotEmpty) return 'ssh://$sshTarget';
    return url.replaceFirst(RegExp(r'^https?://'), '');
  }

  String get latencyDisplay => '${latencyMs}ms';

  Color get statusColor => online ? AppColors.statusEmerald : AppColors.statusCoral;

  AckbarSessionStatus get badgeStatus => online ? AckbarSessionStatus.active : AckbarSessionStatus.offline;

  HostStatusIndicator toIndicator() {
    return HostStatusIndicator(
      name: name,
      isOnline: online,
      latency: latencyDisplay,
    );
  }

  factory HostRecord.fromJson(Map<String, dynamic> json) {
    DateTime parseDate(dynamic val) {
      if (val == null) return DateTime.now();
      return DateTime.tryParse(val.toString()) ?? DateTime.now();
    }

    return HostRecord(
      name: json['name']?.toString() ?? 'unknown',
      url: json['url']?.toString() ?? 'http://127.0.0.1:7777',
      sshTarget: json['ssh_target']?.toString() ?? '',
      remoteCwd: json['remote_cwd']?.toString() ?? json['projects_dir']?.toString() ?? '~/Work',
      online: json['online'] != false,
      latencyMs: json['latency_ms'] is int
          ? json['latency_ms'] as int
          : int.tryParse(json['latency_ms']?.toString() ?? '') ?? 1,
      version: json['version']?.toString() ?? '',
      uptime: json['uptime']?.toString() ?? 'Active',
      sessionsCount: json['sessions_count'] is int
          ? json['sessions_count'] as int
          : int.tryParse(json['sessions_count']?.toString() ?? '') ?? 0,
      tailscaleIp: json['tailscale_ip']?.toString() ?? '',
      authToken: json['auth_token']?.toString() ?? json['token']?.toString() ?? '',
      createdAt: parseDate(json['created_at']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'name': name,
      'url': url,
      'ssh_target': sshTarget,
      'remote_cwd': remoteCwd,
      'online': online,
      'latency_ms': latencyMs,
      'version': version,
      'uptime': uptime,
      'sessions_count': sessionsCount,
      'tailscale_ip': tailscaleIp,
      'auth_token': authToken,
      'created_at': createdAt.toIso8601String(),
    };
  }

  HostRecord copyWith({
    String? name,
    String? url,
    String? sshTarget,
    String? remoteCwd,
    bool? online,
    int? latencyMs,
    String? version,
    String? uptime,
    int? sessionsCount,
    String? tailscaleIp,
    String? authToken,
    DateTime? createdAt,
  }) {
    return HostRecord(
      name: name ?? this.name,
      url: url ?? this.url,
      sshTarget: sshTarget ?? this.sshTarget,
      remoteCwd: remoteCwd ?? this.remoteCwd,
      online: online ?? this.online,
      latencyMs: latencyMs ?? this.latencyMs,
      version: version ?? this.version,
      uptime: uptime ?? this.uptime,
      sessionsCount: sessionsCount ?? this.sessionsCount,
      tailscaleIp: tailscaleIp ?? this.tailscaleIp,
      authToken: authToken ?? this.authToken,
      createdAt: createdAt ?? this.createdAt,
    );
  }
}
