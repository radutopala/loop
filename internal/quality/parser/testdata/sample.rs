use std::collections::HashMap;
use crate::graph::{Node, edge::{self, Edge as E}};
use super::*;
mod inner;
mod inline {}

pub struct Widget;
enum Shape { A }
trait Area { fn area(&self) -> f64; }
type Alias = u32;

impl Widget {
    pub fn new(x: i32) -> Self {
        helper(x);
        HashMap::new();
        x.clone();
        Widget
    }
}

#[test]
fn it_works() {}
