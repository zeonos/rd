// Client for the RD server's console API, used with the viewer's login token.
import 'dart:convert';

import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/models/platform_model.dart';

import '../utils/http_service.dart' as http;

class RdApiError implements Exception {
  final int status;
  final String message;
  RdApiError(this.status, this.message);
  bool get unauthorized => status == 401;
  @override
  String toString() => message;
}

class RdDevice {
  final String id;
  final String alias;
  final String hostname;
  final String username;
  final String os;
  final String group;
  final bool online;
  final DateTime? lastSeen;
  final bool unattended;

  RdDevice({
    required this.id,
    required this.alias,
    required this.hostname,
    required this.username,
    required this.os,
    required this.group,
    required this.online,
    required this.lastSeen,
    required this.unattended,
  });

  String get name => alias.isNotEmpty
      ? alias
      : (hostname.isNotEmpty ? hostname : id);

  /// "Unassigned" for devices without a location.
  String get location => group.isEmpty ? RdApi.unassigned : group;

  factory RdDevice.fromJson(Map<String, dynamic> j) => RdDevice(
        id: (j['id'] ?? '').toString(),
        alias: (j['alias'] ?? '').toString(),
        hostname: (j['hostname'] ?? '').toString(),
        username: (j['username'] ?? '').toString(),
        os: (j['os'] ?? '').toString(),
        group: (j['group'] ?? '').toString(),
        online: j['online'] == true,
        lastSeen: j['last_seen'] == null
            ? null
            : DateTime.tryParse(j['last_seen'].toString())?.toLocal(),
        unattended: j['unattended'] == true,
      );

  String get lastSeenText {
    if (online) return 'Now';
    final t = lastSeen;
    if (t == null) return 'Never';
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 1) return 'Just now';
    if (d.inMinutes < 60) return '${d.inMinutes} min ago';
    if (d.inHours < 24) return '${d.inHours} h ago';
    if (d.inDays == 1) return 'Yesterday';
    return '${d.inDays} days ago';
  }
}

class RdScript {
  final int id;
  final String name;
  final String shell;
  RdScript(this.id, this.name, this.shell);
}

class RdRun {
  final String status;
  final int? exitCode;
  final String output;
  RdRun(this.status, this.exitCode, this.output);
  bool get finished =>
      status == 'success' ||
      status == 'failed' ||
      status == 'expired' ||
      status == 'cancelled';
}

class RdApi {
  RdApi._();

  static const unassigned = 'Unassigned';

  static String get token => bind.mainGetLocalOption(key: 'access_token');

  static Future<dynamic> _send(String method, String path,
      {Object? body}) async {
    final base = await bind.mainGetApiServer();
    final uri = Uri.parse('$base$path');
    final headers = getHttpHeaders();
    headers['Content-Type'] = 'application/json';
    final resp = method == 'GET'
        ? await http.get(uri, headers: headers)
        : await http.post(uri,
            headers: headers, body: body == null ? null : jsonEncode(body));
    final text = decode_http_response(resp);
    dynamic data;
    try {
      data = text.isEmpty ? null : jsonDecode(text);
    } catch (_) {
      data = null;
    }
    if (resp.statusCode != 200) {
      final msg = (data is Map && data['error'] != null)
          ? data['error'].toString()
          : 'The server answered ${resp.statusCode}.';
      throw RdApiError(resp.statusCode, msg);
    }
    return data;
  }

  static Future<List<RdDevice>> devices() async {
    final data = await _send('GET', '/api/admin/devices');
    final list = (data as List?) ?? [];
    return list
        .map((e) => RdDevice.fromJson(Map<String, dynamic>.from(e as Map)))
        .toList();
  }

  static Future<List<RdScript>> scripts() async {
    final data = await _send('GET', '/api/admin/scripts');
    final list = (data as List?) ?? [];
    return list.map((e) {
      final m = Map<String, dynamic>.from(e as Map);
      return RdScript((m['id'] as num).toInt(), (m['name'] ?? '').toString(),
          (m['shell'] ?? '').toString());
    }).toList();
  }

  /// Starts a job on one device; returns the job id.
  static Future<int> runScript(String deviceId,
      {int? scriptId, String? shell, String? body}) async {
    final req = <String, dynamic>{
      'devices': [deviceId]
    };
    if (scriptId != null) {
      req['script_id'] = scriptId;
    } else {
      req['name'] = 'Run from RD viewer';
      req['shell'] = shell ?? 'powershell';
      req['body'] = body ?? '';
      req['run_as'] = 'system';
    }
    final data = await _send('POST', '/api/admin/jobs', body: req);
    return ((data as Map)['job']['id'] as num).toInt();
  }

  /// action: reboot, reboot-safe-mode, shutdown or logoff.
  static Future<int> power(String deviceId, String action) async {
    final data = await _send('POST', '/api/admin/jobs', body: {
      'devices': [deviceId],
      'action': action,
    });
    return ((data as Map)['job']['id'] as num).toInt();
  }

  static Future<RdRun?> run(int jobId) async {
    final data = await _send('GET', '/api/admin/jobs/$jobId');
    final runs = ((data as Map)['runs'] as List?) ?? [];
    if (runs.isEmpty) return null;
    final m = Map<String, dynamic>.from(runs.first as Map);
    return RdRun((m['status'] ?? '').toString(),
        (m['exit_code'] as num?)?.toInt(), (m['output'] ?? '').toString());
  }
}
