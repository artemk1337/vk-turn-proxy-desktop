# Walk dependency

Source https://github.com/lxn/walk at commit c389da54e794 (2021-01-12).
The original BSD-3-Clause license is in `LICENSE`.

The local patch keeps the layout completion channel open during shutdown and makes completion sends cancellable. Each layout has its own completion channel, so a canceled worker cannot complete a newer layout. Closing a window during an active layout previously let a layout worker send on the closed completion channel.

Form disposal stops accepting new layout requests before shutting down the worker, including size messages delivered during native window destruction.
