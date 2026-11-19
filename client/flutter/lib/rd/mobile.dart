// Android/iOS: sign-in, then the device list by location.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/formatter/id_formatter.dart';
import 'package:flutter_hbb/models/ab_model.dart';
import 'package:get/get.dart';

import 'actions.dart';
import 'api.dart';
import 'sign_in.dart';
import 'theme.dart';

class RdMobileShell extends StatelessWidget {
  const RdMobileShell({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Obx(() => gFFI.userModel.userName.value.isEmpty
        ? const Scaffold(
            backgroundColor: Rd.mist,
            body: SafeArea(child: RdSignInPage()))
        : const RdMobileHome());
  }
}

class RdMobileHome extends StatefulWidget {
  const RdMobileHome({Key? key}) : super(key: key);

  @override
  State<RdMobileHome> createState() => _RdMobileHomeState();
}

class _RdMobileHomeState extends State<RdMobileHome> {
  final _search = TextEditingController();
  List<RdDevice> _devices = [];
  bool _loading = true;
  String? _error;
  String _location = '';
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _load(pullAb: true);
    _timer = Timer.periodic(const Duration(seconds: 20), (_) => _load());
  }

  @override
  void dispose() {
    _timer?.cancel();
    _search.dispose();
    super.dispose();
  }

  Future<void> _load({bool pullAb = false}) async {
    if (RdApi.token.isEmpty) {
      Future.delayed(const Duration(milliseconds: 400), () {
        if (mounted) _load(pullAb: pullAb);
      });
      return;
    }
    if (pullAb) {
      gFFI.abModel.pullAb(force: ForcePullAb.listAndCurrent, quiet: true);
    }
    try {
      final list = await RdApi.devices();
      if (!mounted) return;
      setState(() {
        _devices = list;
        _loading = false;
        _error = null;
      });
    } on RdApiError catch (e) {
      if (!mounted) return;
      if (e.unauthorized) {
        await gFFI.userModel.reset(resetOther: true);
        return;
      }
      setState(() {
        _loading = false;
        _error = e.message;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Could not reach the RD server.';
      });
    }
  }

  bool _inLocation(RdDevice d) =>
      _location.isEmpty ||
      d.location == _location ||
      d.location.startsWith('$_location/');

  List<RdDevice> get _visible {
    final q = _search.text.trim().toLowerCase();
    final qDigits = q.replaceAll(' ', '');
    final l = _devices.where((d) {
      if (!_inLocation(d)) return false;
      if (q.isEmpty) return true;
      return d.name.toLowerCase().contains(q) ||
          d.id.contains(qDigits) ||
          d.username.toLowerCase().contains(q) ||
          d.ip.contains(q);
    }).toList();
    l.sort((a, b) {
      if (a.online != b.online) return a.online ? -1 : 1;
      return a.name.toLowerCase().compareTo(b.name.toLowerCase());
    });
    return l;
  }

  List<String> get _locations {
    final set = <String>{};
    for (final d in _devices) {
      final parts = d.location.split('/');
      for (var i = 1; i <= parts.length; i++) {
        set.add(parts.sublist(0, i).join('/'));
      }
    }
    return set.toList()..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));
  }

  void _openDevice(RdDevice d) {
    showModalBottomSheet(
      context: context,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(children: [
                Rd.statusDot(d.online),
                const SizedBox(width: 8),
                Text(d.online ? 'Online' : 'Offline, ${d.lastSeenText}',
                    style: TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                        color: d.online ? Rd.online : Rd.muted)),
              ]),
              const SizedBox(height: 6),
              Text(d.name,
                  style: const TextStyle(
                      fontSize: 22, fontWeight: FontWeight.w600, color: Rd.ink)),
              Text('${formatID(d.id)}  ·  ${d.location}',
                  style: const TextStyle(fontSize: 14, color: Rd.muted)),
              if (d.os.isNotEmpty || d.username.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                      [d.os, if (d.username.isNotEmpty) 'User: ${d.username}']
                          .where((e) => e.isNotEmpty)
                          .join('\n'),
                      style: const TextStyle(fontSize: 14, color: Rd.body)),
                ),
              const SizedBox(height: 16),
              ElevatedButton.icon(
                style: Rd.primary.copyWith(
                    minimumSize: const MaterialStatePropertyAll(Size(0, 52))),
                onPressed: () {
                  Navigator.of(ctx).pop();
                  connect(context, d.id);
                },
                icon: const Icon(Icons.desktop_windows_outlined),
                label: const Text('Connect'),
              ),
              const SizedBox(height: 8),
              Row(children: [
                Expanded(
                  child: OutlinedButton.icon(
                    style: Rd.secondary.copyWith(
                        minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
                    onPressed: () {
                      Navigator.of(ctx).pop();
                      connect(context, d.id, isFileTransfer: true);
                    },
                    icon: const Icon(Icons.folder_outlined, size: 18),
                    label: const Text('Files'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton.icon(
                    style: Rd.secondary.copyWith(
                        minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
                    onPressed: () {
                      Navigator.of(ctx).pop();
                      rdRunScript(context, deviceId: d.id, name: d.name);
                    },
                    icon: const Icon(Icons.terminal, size: 18),
                    label: const Text('Script'),
                  ),
                ),
              ]),
              const SizedBox(height: 8),
              Row(children: [
                Expanded(
                  child: OutlinedButton.icon(
                    style: Rd.secondary.copyWith(
                        minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
                    onPressed: () {
                      Navigator.of(ctx).pop();
                      rdConfirmPower(context,
                          deviceId: d.id,
                          name: d.name,
                          action: 'reboot',
                          online: d.online);
                    },
                    icon: const Icon(Icons.restart_alt, size: 18),
                    label: const Text('Reboot'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton.icon(
                    style: Rd.secondary.copyWith(
                        minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
                    onPressed: () {
                      Navigator.of(ctx).pop();
                      rdConfirmPower(context,
                          deviceId: d.id,
                          name: d.name,
                          action: 'shutdown',
                          online: d.online);
                    },
                    icon: const Icon(Icons.power_settings_new, size: 18),
                    label: const Text('Shut down'),
                  ),
                ),
              ]),
            ],
          ),
        ),
      ),
    );
  }

  Widget _tile(RdDevice d) {
    return Material(
      color: Colors.white,
      child: InkWell(
        onTap: () => _openDevice(d),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          decoration: const BoxDecoration(
              border: Border(bottom: BorderSide(color: Color(0xFFE6ECEA)))),
          child: Row(children: [
            Rd.statusDot(d.online),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(d.name,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 16,
                          fontWeight: FontWeight.w600,
                          color: Rd.ink)),
                  const SizedBox(height: 2),
                  Text(
                      _location.isEmpty
                          ? '${d.location}  ·  ${d.lastSeenText}'
                          : d.lastSeenText,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 13, color: Rd.muted)),
                ],
              ),
            ),
            Text(formatID(d.id), style: Rd.monoText.copyWith(fontSize: 13)),
          ]),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final rows = _visible;
    final locs = _locations;
    return Scaffold(
      backgroundColor: Rd.mist,
      appBar: AppBar(
        backgroundColor: Rd.ink,
        foregroundColor: Colors.white,
        titleSpacing: 16,
        title: Row(children: [
          Rd.mark(26),
          const SizedBox(width: 10),
          const Text('RD',
              style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
        ]),
        actions: [
          PopupMenuButton<String>(
            icon: const Icon(Icons.account_circle_outlined),
            onSelected: (v) {
              if (v == 'signout') gFFI.userModel.logOut();
            },
            itemBuilder: (_) => [
              PopupMenuItem(
                  enabled: false,
                  child: Text('Signed in as ${gFFI.userModel.userName.value}')),
              const PopupMenuItem(value: 'signout', child: Text('Sign out')),
            ],
          ),
        ],
      ),
      body: Column(children: [
        Container(
          color: Rd.ink,
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
          child: TextField(
            controller: _search,
            onChanged: (_) => setState(() {}),
            style: const TextStyle(color: Colors.white, fontSize: 16),
            cursorColor: Colors.white,
            decoration: InputDecoration(
              hintText: 'Search name, ID, user or IP',
              hintStyle: const TextStyle(color: Rd.onInkMuted),
              prefixIcon: const Icon(Icons.search, color: Rd.onInkMuted),
              filled: true,
              fillColor: Rd.inkRaised,
              isDense: true,
              border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(10),
                  borderSide: BorderSide.none),
            ),
          ),
        ),
        SizedBox(
          height: 52,
          child: ListView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            children: [
              _chip('', 'All'),
              ...locs.map((l) => _chip(l, l.split('/').last)),
            ],
          ),
        ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(_error!,
                style: const TextStyle(fontSize: 14, color: Rd.danger)),
          ),
        Expanded(
          child: RefreshIndicator(
            color: Rd.fjord,
            onRefresh: () => _load(pullAb: true),
            child: _loading
                ? ListView(children: const [
                    Padding(
                        padding: EdgeInsets.all(40),
                        child: Center(
                            child: CircularProgressIndicator(color: Rd.fjord)))
                  ])
                : rows.isEmpty
                    ? ListView(children: [
                        Padding(
                          padding: const EdgeInsets.all(32),
                          child: Text(
                              _devices.isEmpty
                                  ? 'No devices yet.'
                                  : 'No device matches.',
                              style: const TextStyle(
                                  fontSize: 15, color: Rd.muted)),
                        )
                      ])
                    : ListView.builder(
                        itemCount: rows.length,
                        itemBuilder: (_, i) => _tile(rows[i])),
          ),
        ),
      ]),
    );
  }

  Widget _chip(String loc, String label) {
    final on = _location == loc;
    final inLoc = _devices
        .where((d) => loc.isEmpty || d.location == loc || d.location.startsWith('$loc/'))
        .toList();
    final online = inLoc.where((d) => d.online).length;
    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: ChoiceChip(
        selected: on,
        onSelected: (_) => setState(() => _location = loc),
        label: Text('$label  $online/${inLoc.length}'),
        selectedColor: Rd.fjord,
        backgroundColor: Colors.white,
        side: const BorderSide(color: Rd.lineStrong),
        labelStyle: TextStyle(
            color: on ? Colors.white : Rd.ink,
            fontWeight: on ? FontWeight.w600 : FontWeight.w500),
        showCheckmark: false,
      ),
    );
  }
}
