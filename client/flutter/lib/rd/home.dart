// Home screen: devices from the RD server, grouped by location.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/formatter/id_formatter.dart';
import 'package:flutter_hbb/desktop/pages/desktop_tab_page.dart';
import 'package:flutter_hbb/models/ab_model.dart';
import 'package:flutter_hbb/models/platform_model.dart';

import 'actions.dart';
import 'api.dart';
import 'theme.dart';

const _allLocations = '';

class RdHomePage extends StatefulWidget {
  const RdHomePage({Key? key}) : super(key: key);

  @override
  State<RdHomePage> createState() => _RdHomePageState();
}

class _RdHomePageState extends State<RdHomePage> {
  final _search = TextEditingController();
  List<RdDevice> _devices = [];
  bool _loading = true;
  String? _error;
  String _location = _allLocations;
  String? _selectedId;
  String _myId = '';
  Timer? _timer;
  int _ticks = 0;

  @override
  void initState() {
    super.initState();
    bind.mainGetMyId().then((v) {
      if (mounted) setState(() => _myId = v);
    });
    _load();
    _timer = Timer.periodic(const Duration(seconds: 15), (_) {
      _ticks++;
      // Pull the address book every 5 minutes: it carries the unattended
      // password hashes the connection uses.
      _load(pullAb: _ticks % 20 == 0);
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    _search.dispose();
    super.dispose();
  }

  Future<void> _load({bool pullAb = false}) async {
    if (RdApi.token.isEmpty) {
      // Sign-in stores the token right after the user appears; try again shortly.
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
        _selectedId ??= list.isNotEmpty ? _sorted(list).first.id : null;
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
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = 'Could not reach the RD server. Showing the last list.';
      });
    }
  }

  List<RdDevice> _sorted(List<RdDevice> list) {
    final l = List<RdDevice>.from(list);
    l.sort((a, b) {
      if (a.online != b.online) return a.online ? -1 : 1;
      return a.name.toLowerCase().compareTo(b.name.toLowerCase());
    });
    return l;
  }

  bool _inLocation(RdDevice d, String loc) =>
      loc == _allLocations ||
      d.location == loc ||
      d.location.startsWith('$loc/');

  List<RdDevice> get _visible {
    final q = _search.text.trim().toLowerCase();
    final qDigits = q.replaceAll(' ', '');
    return _sorted(_devices.where((d) {
      if (!_inLocation(d, _location)) return false;
      if (q.isEmpty) return true;
      return d.name.toLowerCase().contains(q) ||
          d.hostname.toLowerCase().contains(q) ||
          d.id.contains(qDigits) ||
          d.username.toLowerCase().contains(q);
    }).toList());
  }

  /// Every location path and its parents, sorted.
  List<String> get _locations {
    final set = <String>{};
    for (final d in _devices) {
      final parts = d.location.split('/');
      for (var i = 1; i <= parts.length; i++) {
        set.add(parts.sublist(0, i).join('/'));
      }
    }
    final l = set.toList()..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));
    l.remove(RdApi.unassigned);
    if (set.contains(RdApi.unassigned)) l.add(RdApi.unassigned);
    return l;
  }

  RdDevice? get _selected {
    for (final d in _devices) {
      if (d.id == _selectedId) return d;
    }
    return null;
  }

  void _connect(RdDevice d, {bool files = false}) {
    connect(context, d.id, isFileTransfer: files);
  }

  // ---------------------------------------------------------------- header

  Widget _header() {
    return Container(
      color: Rd.ink,
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
      child: Row(children: [
        Rd.mark(28),
        const SizedBox(width: 10),
        const Text('RD',
            style: TextStyle(
                color: Colors.white,
                fontSize: 17,
                fontWeight: FontWeight.w600)),
        const SizedBox(width: 20),
        Expanded(
          child: Align(
            alignment: Alignment.centerLeft,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: SizedBox(
                height: 40,
                child: TextField(
                  controller: _search,
                  onChanged: (_) => setState(() {}),
                  style: const TextStyle(color: Colors.white, fontSize: 15),
                  cursorColor: Colors.white,
                  decoration: InputDecoration(
                    hintText: 'Search name, ID or user',
                    hintStyle: const TextStyle(color: Rd.onInkMuted),
                    prefixIcon: const Icon(Icons.search,
                        color: Rd.onInkMuted, size: 20),
                    filled: true,
                    fillColor: Rd.inkRaised,
                    isDense: true,
                    contentPadding: const EdgeInsets.symmetric(vertical: 10),
                    border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(8),
                        borderSide: BorderSide.none),
                    focusedBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(8),
                        borderSide:
                            const BorderSide(color: Rd.signal, width: 2)),
                  ),
                ),
              ),
            ),
          ),
        ),
        const SizedBox(width: 16),
        if (_myId.isNotEmpty)
          Text.rich(
            TextSpan(children: [
              const TextSpan(text: 'This PC: '),
              TextSpan(
                  text: formatID(_myId),
                  style:
                      const TextStyle(fontFamily: Rd.mono, color: Colors.white)),
            ]),
            style: const TextStyle(color: Rd.onInkMuted, fontSize: 14),
          ),
        const SizedBox(width: 12),
        PopupMenuButton<String>(
          tooltip: 'Account',
          offset: const Offset(0, 44),
          onSelected: (v) {
            if (v == 'settings') DesktopTabPage.onAddSetting();
            if (v == 'signout') gFFI.userModel.logOut();
          },
          itemBuilder: (_) => [
            PopupMenuItem(
                enabled: false,
                child: Text('Signed in as ${gFFI.userModel.userName.value}',
                    style: const TextStyle(color: Rd.muted))),
            const PopupMenuItem(value: 'settings', child: Text('Settings')),
            const PopupMenuItem(value: 'signout', child: Text('Sign out')),
          ],
          child: CircleAvatar(
            radius: 18,
            backgroundColor: const Color(0xFF2F454B),
            child: Text(
              gFFI.userModel.userName.value.isEmpty
                  ? '?'
                  : gFFI.userModel.userName.value[0].toUpperCase(),
              style: const TextStyle(
                  color: Colors.white, fontWeight: FontWeight.w600),
            ),
          ),
        ),
      ]),
    );
  }

  // --------------------------------------------------------------- sidebar

  Widget _locationButton(String loc, String label, int depth) {
    final inLoc = _devices.where((d) => _inLocation(d, loc)).toList();
    final online = inLoc.where((d) => d.online).length;
    final current = _location == loc;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Material(
        color: current ? Rd.mistDark : Colors.transparent,
        borderRadius: BorderRadius.circular(8),
        child: InkWell(
          borderRadius: BorderRadius.circular(8),
          hoverColor: const Color(0xFFE1E8E6),
          onTap: () => setState(() => _location = loc),
          child: Container(
            height: 40,
            padding: EdgeInsets.only(left: 12.0 + depth * 14, right: 12),
            decoration: current
                ? const BoxDecoration(
                    border: Border(left: BorderSide(color: Rd.fjord, width: 3)))
                : null,
            child: Row(children: [
              Expanded(
                child: Text(label,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontSize: 15,
                        color: Rd.ink,
                        fontWeight:
                            current ? FontWeight.w600 : FontWeight.w400)),
              ),
              Text('$online/${inLoc.length}',
                  style: const TextStyle(fontSize: 13, color: Rd.muted)),
            ]),
          ),
        ),
      ),
    );
  }

  Widget _sidebar() {
    return Container(
      width: 240,
      decoration: const BoxDecoration(
          border: Border(right: BorderSide(color: Rd.line))),
      child: ListView(
        padding: const EdgeInsets.fromLTRB(12, 20, 12, 20),
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(12, 0, 12, 8),
            child: Text('Locations',
                style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: Rd.muted)),
          ),
          _locationButton(_allLocations, 'All devices', 0),
          ..._locations.map((loc) {
            final parts = loc.split('/');
            return _locationButton(loc, parts.last, parts.length - 1);
          }),
          const Padding(
            padding: EdgeInsets.all(12),
            child: Text('Locations come from the groups in the RD console.',
                style:
                    TextStyle(fontSize: 13, height: 1.5, color: Rd.muted)),
          ),
        ],
      ),
    );
  }

  // ------------------------------------------------------------ device list

  Widget _cells(List<Widget> c) {
    return Row(children: [
      SizedBox(width: 28, child: c[0]),
      const SizedBox(width: 12),
      Expanded(flex: 14, child: c[1]),
      const SizedBox(width: 12),
      SizedBox(width: 120, child: c[2]),
      const SizedBox(width: 12),
      Expanded(flex: 10, child: c[3]),
      const SizedBox(width: 12),
      Expanded(flex: 11, child: c[4]),
      const SizedBox(width: 12),
      SizedBox(width: 100, child: c[5]),
    ]);
  }

  Widget _row(RdDevice d) {
    final selected = d.id == _selectedId;
    const cell = TextStyle(fontSize: 14, color: Rd.body);
    return Material(
      color: selected ? Rd.selected : Colors.white,
      child: InkWell(
        onTap: () => setState(() => _selectedId = d.id),
        onDoubleTap: () => _connect(d),
        hoverColor: const Color(0xFFF2F6F5),
        child: Container(
          constraints: const BoxConstraints(minHeight: 48),
          padding: const EdgeInsets.symmetric(horizontal: 16),
          decoration: const BoxDecoration(
              border: Border(bottom: BorderSide(color: Color(0xFFE6ECEA)))),
          child: _cells([
            Center(child: Rd.statusDot(d.online)),
            Text(d.name,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    fontSize: 15, fontWeight: FontWeight.w600, color: Rd.ink)),
            Text(formatID(d.id), style: Rd.monoText),
            Text(d.location, overflow: TextOverflow.ellipsis, style: cell),
            Text(d.os, overflow: TextOverflow.ellipsis, style: cell),
            Text(d.lastSeenText,
                style: const TextStyle(fontSize: 14, color: Rd.muted)),
          ]),
        ),
      ),
    );
  }

  Widget _list() {
    final rows = _visible;
    final online = rows.where((d) => d.online).length;
    final title = _location == _allLocations
        ? 'All devices'
        : _location.split('/').last;
    Widget body;
    if (_loading) {
      body = const Padding(
          padding: EdgeInsets.all(32),
          child: Center(child: CircularProgressIndicator(color: Rd.fjord)));
    } else if (_devices.isEmpty) {
      body = const Padding(
        padding: EdgeInsets.all(32),
        child: Text(
            'No devices yet. Install RD on a machine with install.ps1 and it shows up here.',
            style: TextStyle(fontSize: 15, color: Rd.muted)),
      );
    } else if (rows.isEmpty) {
      body = Padding(
        padding: const EdgeInsets.all(32),
        child: Text(
            _search.text.trim().isEmpty
                ? 'No devices in this location.'
                : 'No device matches "${_search.text.trim()}". Search covers name, ID and user.',
            style: const TextStyle(fontSize: 15, color: Rd.muted)),
      );
    } else {
      body = ListView.builder(
          itemCount: rows.length, itemBuilder: (_, i) => _row(rows[i]));
    }
    return Padding(
      padding: const EdgeInsets.fromLTRB(28, 24, 28, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(crossAxisAlignment: CrossAxisAlignment.end, children: [
            Text(title,
                style: const TextStyle(
                    fontSize: 26, fontWeight: FontWeight.w600, color: Rd.ink)),
            const SizedBox(width: 12),
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text('$online of ${rows.length} online',
                  style: const TextStyle(fontSize: 15, color: Rd.muted)),
            ),
            const Spacer(),
            IconButton(
              tooltip: 'Refresh',
              onPressed: () => _load(pullAb: true),
              icon: const Icon(Icons.refresh, color: Rd.muted),
            ),
          ]),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(_error!,
                  style: const TextStyle(fontSize: 14, color: Rd.danger)),
            ),
          const SizedBox(height: 16),
          Expanded(
            child: Container(
              clipBehavior: Clip.antiAlias,
              decoration: BoxDecoration(
                color: Colors.white,
                borderRadius: BorderRadius.circular(10),
                border: Border.all(color: Rd.line),
              ),
              child: Column(children: [
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  decoration: const BoxDecoration(
                      border: Border(bottom: BorderSide(color: Rd.line))),
                  child: DefaultTextStyle(
                    style: const TextStyle(
                        fontFamily: Rd.sans,
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                        color: Rd.muted),
                    child: _cells(const [
                      SizedBox(),
                      Text('Name'),
                      Text('ID'),
                      Text('Location'),
                      Text('System'),
                      Text('Last seen'),
                    ]),
                  ),
                ),
                Expanded(child: body),
              ]),
            ),
          ),
          const SizedBox(height: 8),
          const Text('Double-click a device to connect.',
              style: TextStyle(fontSize: 13, color: Rd.muted)),
        ],
      ),
    );
  }

  // ---------------------------------------------------------- detail panel

  Widget _action(IconData icon, String label, VoidCallback onPressed) {
    return OutlinedButton.icon(
      style: Rd.secondary.copyWith(
          minimumSize: const MaterialStatePropertyAll(Size(0, 44))),
      onPressed: onPressed,
      icon: Icon(icon, size: 18),
      label: Text(label),
    );
  }

  Widget _fact(String k, String v, {bool mono = false}) => Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          SizedBox(
              width: 96,
              child: Text(k,
                  style: const TextStyle(fontSize: 14, color: Rd.muted))),
          Expanded(
              child: SelectableText(v.isEmpty ? '–' : v,
                  style: mono
                      ? Rd.monoText.copyWith(color: Rd.ink)
                      : const TextStyle(fontSize: 14, color: Rd.ink))),
        ]),
      );

  Widget _detail() {
    final d = _selected;
    return Container(
      width: 320,
      decoration: const BoxDecoration(
          color: Colors.white,
          border: Border(left: BorderSide(color: Rd.line))),
      padding: const EdgeInsets.all(24),
      child: d == null
          ? const Text('Select a device to see its details.',
              style: TextStyle(fontSize: 15, color: Rd.muted))
          : SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(children: [
                    Rd.statusDot(d.online),
                    const SizedBox(width: 8),
                    Text(d.online ? 'Online' : 'Offline',
                        style: TextStyle(
                            fontSize: 14,
                            fontWeight: FontWeight.w600,
                            color: d.online ? Rd.online : Rd.muted)),
                  ]),
                  const SizedBox(height: 6),
                  SelectableText(d.name,
                      style: const TextStyle(
                          fontSize: 24,
                          fontWeight: FontWeight.w600,
                          color: Rd.ink)),
                  Row(children: [
                    Text(formatID(d.id),
                        style: Rd.monoText.copyWith(fontSize: 15)),
                    IconButton(
                      tooltip: 'Copy ID',
                      iconSize: 16,
                      onPressed: () =>
                          Clipboard.setData(ClipboardData(text: d.id)),
                      icon: const Icon(Icons.copy, color: Rd.muted),
                    ),
                  ]),
                  const SizedBox(height: 16),
                  ElevatedButton.icon(
                    style: Rd.primary.copyWith(
                        minimumSize:
                            const MaterialStatePropertyAll(Size(0, 48))),
                    onPressed: () => _connect(d),
                    icon: const Icon(Icons.desktop_windows_outlined, size: 20),
                    label: const Text('Connect'),
                  ),
                  if (!d.unattended)
                    const Padding(
                      padding: EdgeInsets.only(top: 8),
                      child: Text(
                          'No unattended password reported yet: you may be asked for one.',
                          style: TextStyle(fontSize: 13, color: Rd.muted)),
                    ),
                  const SizedBox(height: 12),
                  Row(children: [
                    Expanded(
                        child: _action(Icons.folder_outlined, 'Files',
                            () => _connect(d, files: true))),
                    const SizedBox(width: 8),
                    Expanded(
                        child: _action(Icons.terminal, 'Run script',
                            () => rdRunScript(context,
                                deviceId: d.id, name: d.name))),
                  ]),
                  const SizedBox(height: 8),
                  Row(children: [
                    Expanded(
                        child: _action(
                            Icons.restart_alt,
                            'Reboot',
                            () => rdConfirmPower(context,
                                deviceId: d.id,
                                name: d.name,
                                action: 'reboot',
                                online: d.online))),
                    const SizedBox(width: 8),
                    Expanded(
                        child: _action(
                            Icons.power_settings_new,
                            'Shut down',
                            () => rdConfirmPower(context,
                                deviceId: d.id,
                                name: d.name,
                                action: 'shutdown',
                                online: d.online))),
                  ]),
                  const SizedBox(height: 24),
                  _fact('Location', d.location),
                  _fact('System', d.os),
                  _fact('User', d.username),
                  _fact('Host name', d.hostname),
                  _fact('Last seen', d.lastSeenText),
                ],
              ),
            ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      color: Rd.mist,
      child: Column(children: [
        _header(),
        Expanded(
          child: Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            _sidebar(),
            Expanded(child: _list()),
            _detail(),
          ]),
        ),
      ]),
    );
  }
}
