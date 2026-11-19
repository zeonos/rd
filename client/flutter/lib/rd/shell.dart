// The main window's content: sign-in until there is an account, then home.
import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:get/get.dart';
import 'package:window_manager/window_manager.dart';

import 'home.dart';
import 'sign_in.dart';

class RdShell extends StatelessWidget {
  const RdShell({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Obx(() => gFFI.userModel.userName.value.isEmpty
        ? const RdSignInPage()
        : const RdHomePage());
  }
}

/// The home screen needs room for the location list, the device table and
/// the detail panel: grow a smaller (e.g. restored RustDesk-sized) window.
Future<void> rdEnsureMainWindowSize() async {
  try {
    await windowManager.setMinimumSize(const Size(960, 600));
    final size = await windowManager.getSize();
    if (size.width < 1180 || size.height < 720) {
      await windowManager.setSize(const Size(1280, 800));
      await windowManager.center();
    }
  } catch (e) {
    debugPrint('rdEnsureMainWindowSize: $e');
  }
}
