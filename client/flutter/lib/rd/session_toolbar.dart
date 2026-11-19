// Fixed toolbar above the remote screen: screens, Ctrl+Alt+Del, scale,
// quality, and the less frequent actions under More.
import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/shared_state.dart';
import 'package:flutter_hbb/common/widgets/dialog.dart';
import 'package:flutter_hbb/consts.dart';
import 'package:flutter_hbb/models/model.dart';
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:get/get.dart';

import 'actions.dart';
import 'theme.dart';

const double _kHeight = 48;

/// The RD toolbar replaces RustDesk's floating one in remote sessions.
const bool rdSessionToolbarShown = true;

/// Height the toolbar takes from the remote canvas (remote windows only).
double rdSessionToolbarHeight() =>
    desktopType == DesktopType.remote ? _kHeight : 0;

class RdSessionToolbar extends StatefulWidget {
  final String id;
  final FFI ffi;
  const RdSessionToolbar({Key? key, required this.id, required this.ffi})
      : super(key: key);

  @override
  State<RdSessionToolbar> createState() => _RdSessionToolbarState();
}

class _RdSessionToolbarState extends State<RdSessionToolbar> {
  String _scale = '';
  String _quality = '';

  FFI get ffi => widget.ffi;

  @override
  void initState() {
    super.initState();
    _readSettings();
  }

  Future<void> _readSettings() async {
    final s = await bind.sessionGetViewStyle(sessionId: ffi.sessionId) ?? '';
    final q = await bind.sessionGetImageQuality(sessionId: ffi.sessionId) ?? '';
    if (mounted) {
      setState(() {
        _scale = s;
        _quality = q;
      });
    }
  }

  Future<void> _setScale(String v) async {
    await bind.sessionSetViewStyle(sessionId: ffi.sessionId, value: v);
    await ffi.canvasModel.updateViewStyle();
    if (mounted) setState(() => _scale = v);
  }

  Future<void> _setQuality(String v) async {
    await bind.sessionSetImageQuality(sessionId: ffi.sessionId, value: v);
    if (mounted) setState(() => _quality = v);
  }

  void _sendCad() {
    bind.sessionCtrlAltDel(sessionId: ffi.sessionId);
    final m = ScaffoldMessenger.maybeOf(context);
    m?.showSnackBar(const SnackBar(
      content: Text('Sent Ctrl+Alt+Del'),
      backgroundColor: Rd.ink,
      behavior: SnackBarBehavior.floating,
      width: 260,
      duration: Duration(seconds: 2),
    ));
  }

  void _transferFiles() {
    final connToken = bind.sessionGetConnToken(sessionId: ffi.sessionId);
    connect(context, widget.id, isFileTransfer: true, connToken: connToken);
  }

  String get _name {
    final pi = ffi.ffiModel.pi;
    return pi.hostname.isNotEmpty ? pi.hostname : widget.id;
  }

  Widget _label(String text) => Padding(
        padding: const EdgeInsets.only(right: 8),
        child: Text(text,
            style: const TextStyle(
                fontFamily: Rd.sans,
                fontSize: 13,
                fontWeight: FontWeight.w500,
                color: Rd.muted)),
      );

  /// A row of joined toggle buttons.
  Widget _segments(List<_Seg> segs) {
    final children = <Widget>[];
    for (var i = 0; i < segs.length; i++) {
      final s = segs[i];
      children.add(Semantics(
        selected: s.on,
        button: true,
        child: Material(
          color: s.on ? Rd.fjord : Colors.white,
          child: InkWell(
            onTap: s.onTap,
            hoverColor: s.on ? Rd.fjordDark : const Color(0xFFE1E8E6),
            child: Container(
              height: 34,
              constraints: const BoxConstraints(minWidth: 40),
              padding: const EdgeInsets.symmetric(horizontal: 12),
              alignment: Alignment.center,
              decoration: i == 0
                  ? null
                  : const BoxDecoration(
                      border: Border(left: BorderSide(color: Rd.lineStrong))),
              child: Text(s.label,
                  style: TextStyle(
                      fontFamily: Rd.sans,
                      fontSize: 14,
                      fontWeight: s.on ? FontWeight.w600 : FontWeight.w500,
                      color: s.on ? Colors.white : Rd.ink)),
            ),
          ),
        ),
      ));
    }
    return Container(
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(
        border: Border.all(color: Rd.lineStrong),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(mainAxisSize: MainAxisSize.min, children: children),
    );
  }

  Widget _screens() {
    return Obx(() {
      final count = ffi.ffiModel.pi.displays.length;
      if (count <= 1) return const SizedBox.shrink();
      int current = 0;
      try {
        current = CurrentDisplayState.find(widget.id).value;
      } catch (_) {}
      return Row(mainAxisSize: MainAxisSize.min, children: [
        _label('Screen'),
        _segments(List.generate(
            count,
            (i) => _Seg('${i + 1}', current == i, () {
                  openMonitorInTheSameTab(i, ffi, ffi.ffiModel.pi);
                }))),
        const SizedBox(width: 20),
      ]);
    });
  }

  Widget _more() {
    return PopupMenuButton<String>(
      tooltip: 'More',
      offset: const Offset(0, 40),
      onSelected: (v) {
        switch (v) {
          case 'files':
            _transferFiles();
            break;
          case 'script':
            rdRunScript(context, deviceId: widget.id, name: _name);
            break;
          case 'lock':
            bind.sessionLockScreen(sessionId: ffi.sessionId);
            break;
          case 'reboot':
            showRestartRemoteDevice(ffi.ffiModel.pi, widget.id,
                ffi.sessionId, ffi.dialogManager);
            break;
        }
      },
      itemBuilder: (_) => [
        const PopupMenuItem(value: 'files', child: Text('Transfer files')),
        const PopupMenuItem(value: 'script', child: Text('Run script')),
        const PopupMenuItem(value: 'lock', child: Text('Lock screen')),
        PopupMenuItem(
            value: 'reboot',
            child: Text('Reboot $_name',
                style: const TextStyle(color: Rd.danger))),
      ],
      child: Container(
        height: 36,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: BoxDecoration(
          color: Colors.white,
          border: Border.all(color: Rd.lineStrong),
          borderRadius: BorderRadius.circular(8),
        ),
        child: const Row(mainAxisSize: MainAxisSize.min, children: [
          Text('More',
              style: TextStyle(
                  fontFamily: Rd.sans,
                  fontSize: 14,
                  fontWeight: FontWeight.w500,
                  color: Rd.ink)),
          SizedBox(width: 4),
          Icon(Icons.expand_more, size: 18, color: Rd.ink),
        ]),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      height: _kHeight,
      decoration: const BoxDecoration(
        color: Colors.white,
        border: Border(bottom: BorderSide(color: Rd.line)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(children: [
        Expanded(
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(children: [
              _screens(),
              OutlinedButton.icon(
                style: Rd.secondary.copyWith(
                    minimumSize: const MaterialStatePropertyAll(Size(0, 36)),
                    textStyle: const MaterialStatePropertyAll(TextStyle(
                        fontFamily: Rd.sans,
                        fontSize: 14,
                        fontWeight: FontWeight.w600))),
                onPressed: _sendCad,
                icon: const Icon(Icons.keyboard_outlined, size: 18),
                label: const Text('Ctrl+Alt+Del'),
              ),
              const SizedBox(width: 20),
              _label('Scale'),
              _segments([
                _Seg('Fit window', _scale == kRemoteViewStyleAdaptive,
                    () => _setScale(kRemoteViewStyleAdaptive)),
                _Seg('100%', _scale == kRemoteViewStyleOriginal,
                    () => _setScale(kRemoteViewStyleOriginal)),
              ]),
              const SizedBox(width: 20),
              _label('Quality'),
              _segments([
                _Seg('Best', _quality == kRemoteImageQualityBest,
                    () => _setQuality(kRemoteImageQualityBest)),
                _Seg('Balanced', _quality == kRemoteImageQualityBalanced,
                    () => _setQuality(kRemoteImageQualityBalanced)),
                _Seg('Fast', _quality == kRemoteImageQualityLow,
                    () => _setQuality(kRemoteImageQualityLow)),
              ]),
            ]),
          ),
        ),
        const SizedBox(width: 12),
        _more(),
        const SizedBox(width: 8),
        OutlinedButton(
          style: Rd.dangerOutline,
          onPressed: () => closeConnection(id: widget.id),
          child: const Text('Disconnect'),
        ),
      ]),
    );
  }
}

class _Seg {
  final String label;
  final bool on;
  final VoidCallback onTap;
  _Seg(this.label, this.on, this.onTap);
}
