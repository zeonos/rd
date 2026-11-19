// Device actions that go through the RD server's job queue: run a script,
// reboot, shut down. They work without a session and queue for offline devices.
import 'dart:async';

import 'package:flutter/material.dart';

import 'api.dart';
import 'theme.dart';

Future<void> rdConfirmPower(BuildContext context,
    {required String deviceId,
    required String name,
    required String action,
    required bool online}) async {
  final label = action == 'shutdown' ? 'Shut down' : 'Reboot';
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text('$label $name?'),
      content: Text(online
          ? '$name will ${action == 'shutdown' ? 'shut down' : 'restart'} within a few seconds. Anyone signed in on it will lose unsaved work.'
          : '$name is offline. The ${label.toLowerCase()} is queued and runs when it comes back within 24 hours.'),
      actions: [
        TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel')),
        ElevatedButton(
            style: Rd.primary.copyWith(
                backgroundColor: const MaterialStatePropertyAll(Rd.danger)),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(label)),
      ],
    ),
  );
  if (ok != true) return;
  try {
    await RdApi.power(deviceId, action);
    _snack(context, '$label sent to $name.');
  } catch (e) {
    _snack(context, '$label failed: $e');
  }
}

void _snack(BuildContext context, String text) {
  final m = ScaffoldMessenger.maybeOf(context);
  if (m == null) return;
  m.showSnackBar(SnackBar(
    content: Text(text),
    backgroundColor: Rd.ink,
    behavior: SnackBarBehavior.floating,
    width: 420,
  ));
}

Future<void> rdRunScript(BuildContext context,
    {required String deviceId, required String name}) {
  return showDialog(
      context: context,
      builder: (_) => _RunScriptDialog(deviceId: deviceId, name: name));
}

class _RunScriptDialog extends StatefulWidget {
  final String deviceId;
  final String name;
  const _RunScriptDialog({required this.deviceId, required this.name});

  @override
  State<_RunScriptDialog> createState() => _RunScriptDialogState();
}

class _RunScriptDialogState extends State<_RunScriptDialog> {
  final _body = TextEditingController();
  List<RdScript> _scripts = [];
  int? _scriptId; // null = type a script here
  String _shell = 'powershell';
  bool _running = false;
  String? _error;
  RdRun? _run;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    RdApi.scripts().then((v) {
      if (mounted) setState(() => _scripts = v);
    }).catchError((_) {});
  }

  @override
  void dispose() {
    _poll?.cancel();
    _body.dispose();
    super.dispose();
  }

  Future<void> _start() async {
    if (_scriptId == null && _body.text.trim().isEmpty) {
      setState(() => _error = 'Type a script, or pick a saved one.');
      return;
    }
    setState(() {
      _running = true;
      _error = null;
      _run = null;
    });
    try {
      final job = await RdApi.runScript(widget.deviceId,
          scriptId: _scriptId, shell: _shell, body: _body.text);
      _poll = Timer.periodic(const Duration(seconds: 2), (t) async {
        try {
          final r = await RdApi.run(job);
          if (!mounted) return;
          setState(() => _run = r);
          if (r != null && r.finished) {
            t.cancel();
            setState(() => _running = false);
          }
        } catch (_) {}
      });
    } catch (e) {
      setState(() {
        _running = false;
        _error = e.toString();
      });
    }
  }

  String get _statusText {
    final r = _run;
    if (r == null) return 'Sending…';
    switch (r.status) {
      case 'queued':
        return 'Waiting for ${widget.name} to pick it up…';
      case 'sent':
        return 'Running on ${widget.name}…';
      case 'success':
        return 'Finished (exit code ${r.exitCode ?? 0}).';
      case 'failed':
        return 'Failed${r.exitCode != null ? ' (exit code ${r.exitCode})' : ''}.';
      case 'expired':
        return '${widget.name} did not come online in time.';
      case 'cancelled':
        return 'Cancelled.';
    }
    return r.status;
  }

  @override
  Widget build(BuildContext context) {
    final items = <DropdownMenuItem<int?>>[
      const DropdownMenuItem<int?>(value: null, child: Text('Type a script')),
      ..._scripts.map((s) => DropdownMenuItem<int?>(
          value: s.id, child: Text('${s.name} (${s.shell})'))),
    ];
    return AlertDialog(
      title: Text('Run script on ${widget.name}'),
      content: SizedBox(
        width: 560,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(children: [
              Expanded(
                child: DropdownButtonFormField<int?>(
                  value: _scriptId,
                  items: items,
                  decoration: Rd.field(null),
                  onChanged: _running
                      ? null
                      : (v) => setState(() => _scriptId = v),
                ),
              ),
              if (_scriptId == null) ...[
                const SizedBox(width: 12),
                SizedBox(
                  width: 160,
                  child: DropdownButtonFormField<String>(
                    value: _shell,
                    decoration: Rd.field(null),
                    items: const [
                      DropdownMenuItem(
                          value: 'powershell', child: Text('PowerShell')),
                      DropdownMenuItem(value: 'cmd', child: Text('cmd')),
                    ],
                    onChanged: _running
                        ? null
                        : (v) => setState(() => _shell = v ?? 'powershell'),
                  ),
                ),
              ],
            ]),
            if (_scriptId == null) ...[
              const SizedBox(height: 12),
              TextField(
                controller: _body,
                enabled: !_running,
                minLines: 5,
                maxLines: 10,
                style: const TextStyle(fontFamily: Rd.mono, fontSize: 13),
                decoration: Rd.field('hostname; Get-Date'),
              ),
            ],
            const SizedBox(height: 8),
            const Text('Runs as SYSTEM. Offline devices run it when they come back, within 24 hours.',
                style: TextStyle(fontSize: 13, color: Rd.muted)),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: Rd.danger)),
            ],
            if (_running || _run != null) ...[
              const SizedBox(height: 16),
              Text(_statusText,
                  style: const TextStyle(
                      fontSize: 14, fontWeight: FontWeight.w500)),
              if ((_run?.output ?? '').isNotEmpty) ...[
                const SizedBox(height: 8),
                Container(
                  constraints: const BoxConstraints(maxHeight: 260),
                  decoration: BoxDecoration(
                      color: Rd.ink, borderRadius: BorderRadius.circular(8)),
                  padding: const EdgeInsets.all(12),
                  child: SingleChildScrollView(
                    child: SelectableText(_run!.output,
                        style: const TextStyle(
                            fontFamily: Rd.mono,
                            fontSize: 12.5,
                            color: Colors.white)),
                  ),
                ),
              ],
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: Text(_running ? 'Close (keeps running)' : 'Close')),
        ElevatedButton(
          style: Rd.primary,
          onPressed: _running ? null : _start,
          child: Text(_run == null ? 'Run' : 'Run again'),
        ),
      ],
    );
  }
}
