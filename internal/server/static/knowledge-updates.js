window.CWPViews.define(function(view,scope){
 'use strict';
 view.querySelectorAll('form.update-form').forEach(form=>{
  window.CWPForms.bind(form,scope,{feedback:form.querySelector('.update-feedback')});
 });
});
