//! Per-request cancellation crosses the async HTTP / synchronous database boundary.
use std::{
    cell::RefCell,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};
pub type Token = Arc<AtomicBool>;
thread_local! { static CURRENT:RefCell<Option<Token>>=const {RefCell::new(None)}; }
pub fn current() -> Option<Token> {
    CURRENT.with(|slot| slot.borrow().clone())
}
pub fn with_token<T>(token: Token, f: impl FnOnce() -> T) -> T {
    struct Restore(Option<Token>);
    impl Drop for Restore {
        fn drop(&mut self) {
            CURRENT.with(|slot| *slot.borrow_mut() = self.0.take());
        }
    }
    let _restore = Restore(CURRENT.with(|slot| slot.replace(Some(token))));
    f()
}
pub struct CancelOnDrop(pub Token);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.store(true, Ordering::Release);
    }
}
